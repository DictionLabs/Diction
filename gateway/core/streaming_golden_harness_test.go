package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type sgPostFn = func(context.Context, string, string, string) (string, string, error)

type sgClient struct {
	ctx  context.Context
	conn *websocket.Conn
}

func (c sgClient) bin(b []byte)      { _ = c.conn.Write(c.ctx, websocket.MessageBinary, b) }
func (c sgClient) audio(n int)       { c.bin(make([]byte, n)) }
func (c sgClient) text(s string)     { _ = c.conn.Write(c.ctx, websocket.MessageText, []byte(s)) }
func (c sgClient) done()             { c.text(`{"action":"done"}`) }
func (c sgClient) abort()            { _ = c.conn.CloseNow() }
func (c sgClient) pause(ms int)      { time.Sleep(time.Duration(ms) * time.Millisecond) }
func (c sgClient) doneLang(l string) { c.text(`{"action":"done","language":"` + l + `"}`) }

type sgScenario struct {
	name         string
	query        string
	offerOpus    bool
	plainGET     bool
	backends     []goldBackend
	gw           func(g *Gateway)
	pre          func(t *testing.T) func()
	inline, post sgPostFn
	intentHook   func(ctx context.Context, r *http.Request, transcript, contextJSON, intent string) (string, bool)
	maxBody      int64
	client       func(c sgClient)
	// backendLine filters and rewrites the logged backend requests; nil keeps them all.
	backendLine func(line string) (string, bool)
}

func sgOKBackend(text string) []goldBackend {
	return []goldBackend{gb("small", goldOK(text))}
}

func runStreamScenario(t *testing.T, sc sgScenario, factory func(g *Gateway, inline, post sgPostFn) http.HandlerFunc) string {
	t.Helper()
	log := &goldLog{}
	sgGlobalLog = log
	if sc.pre != nil {
		defer sc.pre(t)()
	}
	g := &Gateway{health: newHealthState(), maxBodySize: 10 * 1024 * 1024}
	if sc.maxBody != 0 {
		g.maxBodySize = sc.maxBody
	}
	for i, gbk := range sc.backends {
		hits := 0
		var mu = make(chan struct{}, 1)
		name, handler := gbk.cfg.Name, gbk.handler
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu <- struct{}{}
			hits++
			n := hits
			<-mu
			log.add("backend %s hit %d: %s", name, n, describeBackendRequest(r))
			handler(w, r, n)
		}))
		defer srv.Close()
		cfg := gbk.cfg
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
	g.OnStreamingCodec = func(_ context.Context, codec, neg string) {
		log.add("hook OnStreamingCodec codec=%s negotiation=%q", codec, neg)
	}
	if sc.gw != nil {
		sc.gw(g)
	}

	waitHandlers(t)
	prevErr, prevFail, prevSkip, prevIntent := OnError, OnRequestFailed, OnEnhanceSkipped, ResolveIntent
	OnError = func(_ context.Context, e ErrorEvent) {
		log.add("hook OnError source=%s kind=%s reason=%s endpoint=%s provider=%s status=%d inputChars=%d latencySet=%v hint=%q",
			e.Source, e.Kind, e.Reason, e.Endpoint, e.Provider, e.HTTPStatus, e.InputChars, e.LatencyMs >= 0, e.Hint)
	}
	OnRequestFailed = func(_ context.Context, errType string) { log.add("hook OnRequestFailed type=%s", errType) }
	OnEnhanceSkipped = func(_ context.Context, reason string) { log.add("hook OnEnhanceSkipped reason=%s", reason) }
	ResolveIntent = sc.intentHook
	defer func() {
		waitHandlers(t)
		OnError, OnRequestFailed, OnEnhanceSkipped, ResolveIntent = prevErr, prevFail, prevSkip, prevIntent
	}()

	wrap := func(p sgPostFn) sgPostFn {
		if p == nil {
			return nil
		}
		return func(ctx context.Context, text, ctxJSON, intent string) (string, string, error) {
			_, hasDeadline := ctx.Deadline()
			log.add("postProcess text=%q ctx=%q intent=%q boosted=%v", text, ctxJSON, intent, STTBoosted(ctx))
			_ = hasDeadline
			return p(ctx, text, ctxJSON, intent)
		}
	}
	h := factory(g, wrap(sc.inline), wrap(sc.post))
	srv := httptest.NewServer(trackHandler(h))
	defer srv.Close()

	var sb strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if sc.plainGET {
		resp, err := http.Get(srv.URL + "/v1/audio/stream?" + sc.query)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		fmt.Fprintf(&sb, "http status %d\n", resp.StatusCode)
	} else {
		var opts *websocket.DialOptions
		if sc.offerOpus {
			opts = &websocket.DialOptions{Subprotocols: []string{opusSubprotocol}}
		}
		conn, resp, err := websocket.Dial(ctx, wsURL(srv, sc.query), opts)
		if resp != nil {
			var keys []string
			for k := range resp.Header {
				if strings.HasPrefix(k, "X-Diction-") || k == "Sec-Websocket-Protocol" {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			fmt.Fprintf(&sb, "dial status %d\n", resp.StatusCode)
			for _, k := range keys {
				fmt.Fprintf(&sb, "dial header %s: %s\n", k, strings.Join(resp.Header[k], "|"))
			}
		}
		if err == nil {
			if sc.client != nil {
				sc.client(sgClient{ctx, conn})
			}
			for {
				typ, data, rerr := conn.Read(ctx)
				if rerr != nil {
					var ce websocket.CloseError
					if errors.As(rerr, &ce) {
						fmt.Fprintf(&sb, "close code=%d reason=%q\n", ce.Code, ce.Reason)
					} else {
						fmt.Fprintf(&sb, "read ended without close frame\n")
					}
					break
				}
				if typ == websocket.MessageText && strings.Contains(string(data), `"type":"interim"`) {
					continue
				}
				if typ == websocket.MessageText {
					fmt.Fprintf(&sb, "frame text %s\n", data)
				} else {
					fmt.Fprintf(&sb, "frame binary len=%d\n", len(data))
				}
			}
			_ = conn.CloseNow()
		}
	}
	waitHandlers(t)
	log.mu.Lock()
	for _, l := range log.lines {
		if !strings.HasPrefix(l, "backend ") {
			sb.WriteString(l + "\n")
		}
	}
	for _, l := range log.lines {
		if !strings.HasPrefix(l, "backend ") {
			continue
		}
		if sc.backendLine != nil {
			var keep bool
			if l, keep = sc.backendLine(l); !keep {
				continue
			}
		}
		sb.WriteString(l + "\n")
	}
	log.mu.Unlock()
	return sb.String()
}

func sgFFmpeg(t *testing.T, script string) func() {
	orig := lookupFfmpeg()
	dir := installFakeFfmpeg(t, script)
	testSetFfmpegPath(dir + "/ffmpeg")
	return func() { testSetFfmpegPath(orig) }
}

func sgNoFFmpeg() func() {
	orig := lookupFfmpeg()
	testSetFfmpegPath("")
	return func() { testSetFfmpegPath(orig) }
}

func currentStreamingHandler(g *Gateway, inline, post sgPostFn) http.HandlerFunc {
	return g.StreamingHandlerWithSplitEnhance(inline, post)
}

func renderStreamGolden(t *testing.T, factory func(g *Gateway, inline, post sgPostFn) http.HandlerFunc) string {
	var sb strings.Builder
	for _, sc := range streamScenarios(t) {
		sb.WriteString("=== " + sc.name + "\n")
		sb.WriteString(runStreamScenario(t, sc, factory))
	}
	return sb.String()
}

const streamGoldenPath = "testdata/streaming_golden.txt"

func TestStreamingHandler_GoldenBehaviour(t *testing.T) {
	got := renderStreamGolden(t, currentStreamingHandler)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(streamGoldenPath, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(streamGoldenPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		_ = os.WriteFile(os.Getenv("SG_DUMP"), []byte(got), 0o600)
		gl, wl := strings.Split(got, "\n"), strings.Split(string(want), "\n")
		for i := 0; i < len(gl) && i < len(wl); i++ {
			if gl[i] != wl[i] {
				t.Fatalf("golden mismatch at line %d:\n got: %s\nwant: %s", i+1, gl[i], wl[i])
			}
		}
		t.Fatalf("golden length mismatch: got %d lines, want %d", len(gl), len(wl))
	}
}
