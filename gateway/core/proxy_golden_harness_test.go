package core

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type goldBackend struct {
	cfg     Backend
	handler func(w http.ResponseWriter, r *http.Request, hit int)
}

type goldField struct{ name, value string }

type goldReq struct {
	method      string
	query       string
	headers     map[string]string
	fields      []goldField
	fileName    string
	fileContent []byte
	rawBody     []byte
	rawCT       string
	bodyReader  io.Reader
	canceled    bool
}

type goldScenario struct {
	name        string
	backends    []goldBackend
	gw          func(g *Gateway)
	req         goldReq
	postProcess func(ctx context.Context, text, ctxJSON, intent string) (string, string, error)
	intentHook  func(ctx context.Context, r *http.Request, transcript, contextJSON, intent string) (string, bool)
	maxBody     int64
	pre         func(t *testing.T) func()
}

type goldLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *goldLog) add(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func goldWAV(seconds int) []byte {
	var buf bytes.Buffer
	data := make([]byte, 16000*2*seconds)
	_ = WriteWAVHeader(&buf, len(data))
	buf.Write(data)
	return buf.Bytes()
}

func goldOK(text string) func(w http.ResponseWriter, r *http.Request, hit int) {
	return func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"text":%q}`, text)
	}
}

func goldRaw(status int, body string) func(w http.ResponseWriter, r *http.Request, hit int) {
	return func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}
}

func describeBackendRequest(r *http.Request) string {
	body, _ := io.ReadAll(r.Body)
	media, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s %s ct=%s auth=%q cl=%d", r.Method, r.URL.Path, media, r.Header.Get("Authorization"), r.ContentLength)
	if !strings.HasPrefix(media, "multipart/") {
		fmt.Fprintf(&sb, " body=%q", body)
		return sb.String()
	}
	mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		data, _ := io.ReadAll(part)
		if part.FileName() != "" {
			fmt.Fprintf(&sb, " [file name=%q filename=%q ct=%q len=%d head=%q]", part.FormName(), part.FileName(),
				part.Header.Get("Content-Type"), len(data), data[:min(len(data), 12)])
		} else {
			fmt.Fprintf(&sb, " [%s=%q]", part.FormName(), data)
		}
	}
	return sb.String()
}

func buildGoldRequest(t *testing.T, gr goldReq) *http.Request {
	t.Helper()
	method := gr.method
	if method == "" {
		method = http.MethodPost
	}
	target := "/v1/audio/transcriptions"
	if gr.query != "" {
		target += "?" + gr.query
	}
	var body io.Reader
	ct := gr.rawCT
	switch {
	case gr.bodyReader != nil:
		body = gr.bodyReader
	case gr.rawBody != nil:
		body = bytes.NewReader(gr.rawBody)
	default:
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		for _, f := range gr.fields {
			if err := w.WriteField(f.name, f.value); err != nil {
				t.Fatal(err)
			}
		}
		if gr.fileName != "" {
			fw, err := w.CreateFormFile("file", gr.fileName)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = fw.Write(gr.fileContent)
		}
		_ = w.Close()
		body = &buf
		ct = w.FormDataContentType()
	}
	req := httptest.NewRequest(method, target, body)
	if gr.canceled {
		ctx, cancel := context.WithCancel(req.Context())
		cancel()
		req = req.WithContext(ctx)
	}
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	for k, v := range gr.headers {
		req.Header.Set(k, v)
	}
	return req
}

func describeGoldBody(rr *httptest.ResponseRecorder) string {
	b := rr.Body.Bytes()
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) == nil {
		if _, ok := m["e2e"]; ok {
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			return fmt.Sprintf("<e2e envelope keys=%v>", keys)
		}
	}
	return fmt.Sprintf("%q", b)
}

func runGoldScenario(t *testing.T, sc goldScenario, factory func(g *Gateway, pp func(context.Context, string, string, string) (string, string, error)) http.HandlerFunc) string {
	t.Helper()
	log := &goldLog{}
	if sc.pre != nil {
		defer sc.pre(t)()
	}

	g := &Gateway{health: newHealthState(), maxBodySize: 10 * 1024 * 1024}
	if sc.maxBody != 0 {
		g.maxBodySize = sc.maxBody
	}
	for i, gb := range sc.backends {
		hits := 0
		var mu sync.Mutex
		name := gb.cfg.Name
		handler := gb.handler
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			hits++
			n := hits
			mu.Unlock()
			log.add("backend %s hit %d: %s", name, n, describeBackendRequest(r))
			handler(w, r, n)
		}))
		defer srv.Close()
		cfg := gb.cfg
		cfg.URL = srv.URL
		cfg.Aliases = []string{cfg.Name}
		g.backends = append(g.backends, cfg)
		g.health.set(cfg.Name, true)
		if i == 0 {
			g.defaultModel = cfg.Name
		}
		if i == 1 {
			g.fallbackModel = cfg.Name
		}
	}
	g.OnAudioReceived = func(_ context.Context, ms int64) { log.add("hook OnAudioReceived ms=%d", ms) }
	g.OnTranscription = func(_ context.Context, model string, _ int64, chars int, ms int64, enhance, e2e bool) {
		log.add("hook OnTranscription model=%s chars=%d audioMs=%d enhance=%v e2e=%v", model, chars, ms, enhance, e2e)
	}
	g.OnAutoDetect = func(_ context.Context, tier, lang string) { log.add("hook OnAutoDetect tier=%q lang=%q", tier, lang) }
	if sc.gw != nil {
		sc.gw(g)
	}

	waitHandlers(t)
	prevErr, prevFail, prevSkip, prevIntent := OnError, OnRequestFailed, OnEnhanceSkipped, ResolveIntent
	OnError = func(_ context.Context, e ErrorEvent) {
		log.add("hook OnError source=%s kind=%s endpoint=%s provider=%s status=%d inputChars=%d hint=%q",
			e.Source, e.Kind, e.Endpoint, e.Provider, e.HTTPStatus, e.InputChars, e.Hint)
	}
	OnRequestFailed = func(_ context.Context, errType string) { log.add("hook OnRequestFailed type=%s", errType) }
	OnEnhanceSkipped = func(_ context.Context, reason string) { log.add("hook OnEnhanceSkipped reason=%s", reason) }
	ResolveIntent = sc.intentHook
	defer func() {
		OnError, OnRequestFailed, OnEnhanceSkipped, ResolveIntent = prevErr, prevFail, prevSkip, prevIntent
	}()

	var pp func(context.Context, string, string, string) (string, string, error)
	if sc.postProcess != nil {
		pp = func(ctx context.Context, text, ctxJSON, intent string) (string, string, error) {
			log.add("postProcess text=%q ctx=%q intent=%q boosted=%v", text, ctxJSON, intent, STTBoosted(ctx))
			return sc.postProcess(ctx, text, ctxJSON, intent)
		}
	}

	rr := httptest.NewRecorder()
	factory(g, pp)(rr, buildGoldRequest(t, sc.req))

	var sb strings.Builder
	fmt.Fprintf(&sb, "status %d\n", rr.Code)
	hdrs := rr.Header()
	keys := make([]string, 0, len(hdrs))
	for k := range hdrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == "Date" {
			continue
		}
		v := strings.Join(hdrs[k], "|")
		if k == "X-Diction-Whisper-Ms" || k == "X-Diction-Llm-Ms" {
			v = "<ms>"
		}
		fmt.Fprintf(&sb, "header %s: %s\n", k, v)
	}
	fmt.Fprintf(&sb, "body %s\n", describeGoldBody(rr))
	log.mu.Lock()
	for _, l := range log.lines {
		sb.WriteString(l + "\n")
	}
	log.mu.Unlock()
	return sb.String()
}

func goldE2EKey(t *testing.T) string {
	t.Helper()
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes())
}

func goldProfile(device string, entries ...LangEntry) func(g *Gateway) {
	return func(g *Gateway) {
		g.DeviceHashFromContext = func(context.Context) string { return device }
		g.profileStore = &ProfileStore{
			db:    &atomic.Pointer[sql.DB]{},
			cache: map[string][]LangEntry{device: entries},
			exp:   map[string]time.Time{device: time.Now().Add(time.Hour)},
		}
	}
}
