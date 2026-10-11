package core

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Auto-edit through core.ResolveIntent (.claude/plans/intent-auto-edit-plan.md). The
// contract this pins: a switched request behaves exactly like an explicit intent=edit
// — no raw frame ever, the edit intent reaches post-process — and the client is told,
// early on the socket and on the result. A nil hook (community build) changes nothing.

// stubResolveIntent installs a ResolveIntent that switches every empty intent to edit,
// and restores the previous hook when the test ends. It records the intent it was given.
func stubResolveIntent(t *testing.T, switchIt bool) *string {
	t.Helper()
	prev := ResolveIntent
	t.Cleanup(func() { ResolveIntent = prev })
	seen := new(string)
	ResolveIntent = func(_ context.Context, _ *http.Request, _, _, intent string) (string, bool) {
		*seen = intent
		if switchIt && intent == "" {
			return "edit", true
		}
		return intent, false
	}
	return seen
}

// recordIntent is a post-process that echoes the intent it received as the mode.
func recordIntent(text string) func(context.Context, string, string, string) (string, string, error) {
	return func(_ context.Context, _, _, intent string) (string, string, error) {
		if intent == "" {
			intent = "transcribe"
		}
		return text, intent, nil
	}
}

func TestAutoEdit_SplitStreamSendsIntentFrameAndNoRawFrame(t *testing.T) {
	stubResolveIntent(t, true)
	srv := startSplitServer(t, "translate to spanish", recordIntent("Hola, ¿qué tal?"), recordIntent("Hola, ¿qué tal?"))

	frames := dialAndFinish(t, srv, "model=small&language=en&enhance=true&split_enhance=true&auto_edit=1")
	if len(frames) != 2 {
		t.Fatalf("want [intent, result], got %d frames: %v", len(frames), frames)
	}
	var intent intentFrame
	_ = json.Unmarshal([]byte(frames[0]), &intent)
	if intent != (intentFrame{Type: "intent", Intent: "edit", Source: "auto"}) {
		t.Errorf("first frame = %s, want the intent frame", frames[0])
	}
	var res streamResult
	_ = json.Unmarshal([]byte(frames[1]), &res)
	if res.Mode != "edit" || !res.AutoEdit || res.Text != "Hola, ¿qué tal?" {
		t.Errorf("result = %+v, want mode=edit auto_edit=true with the edited text", res)
	}
	for _, f := range frames {
		if bytes.Contains([]byte(f), []byte("translate to spanish")) {
			t.Errorf("the spoken instruction leaked into a frame: %s", f)
		}
	}
}

func TestAutoEdit_FailedEditOnStreamIsFailedNotRaw(t *testing.T) {
	stubResolveIntent(t, true)
	failing := func(context.Context, string, string, string) (string, string, error) {
		return "", "", errors.New("llm down")
	}
	srv := startSplitServer(t, "translate to spanish", failing, failing)

	frames := dialAndFinish(t, srv, "model=small&language=en&enhance=true&split_enhance=true&auto_edit=1")
	if len(frames) != 2 {
		t.Fatalf("want [intent, failed result], got %d frames: %v", len(frames), frames)
	}
	var res streamResult
	_ = json.Unmarshal([]byte(frames[1]), &res)
	if res.Text != "" || res.Status != "failed" || res.Mode != "edit" || !res.AutoEdit {
		t.Errorf("result = %+v, want empty text, status=failed, mode=edit, auto_edit", res)
	}
}

func TestAutoEdit_KeptRequestIsUnchangedSplitAnswer(t *testing.T) {
	stubResolveIntent(t, false)
	srv := startSplitServer(t, "see you tomorrow", recordIntent("See you tomorrow."), recordIntent("See you tomorrow."))

	frames := dialAndFinish(t, srv, "model=small&language=en&enhance=true&split_enhance=true&auto_edit=1")
	if len(frames) != 2 {
		t.Fatalf("want raw + enhanced, got %d: %v", len(frames), frames)
	}
	for _, f := range frames {
		if bytes.Contains([]byte(f), []byte(`"auto_edit"`)) || bytes.Contains([]byte(f), []byte(`"type":"intent"`)) {
			t.Errorf("unswitched request carried Auto-edit wire data: %s", f)
		}
	}
}

func TestAutoEdit_ExplicitIntentReachesHookUnchanged(t *testing.T) {
	seen := stubResolveIntent(t, true)
	srv := startSplitServer(t, "make it formal", recordIntent("Formal."), recordIntent("Formal."))
	frames := dialAndFinish(t, srv, "model=small&language=en&enhance=true&intent=edit-selected")
	if *seen != "edit-selected" {
		t.Errorf("hook saw intent %q, want edit-selected", *seen)
	}
	if len(frames) != 1 || bytes.Contains([]byte(frames[0]), []byte(`"auto_edit"`)) {
		t.Errorf("explicit intent must not be marked auto: %v", frames)
	}
}

func TestAutoEdit_NilHookIsCommunityBehaviour(t *testing.T) {
	prev := ResolveIntent
	ResolveIntent = nil
	t.Cleanup(func() { ResolveIntent = prev })
	srv := startSplitServer(t, "translate to spanish", recordIntent("translate to Spanish."), recordIntent("translate to Spanish."))
	frames := dialAndFinish(t, srv, "model=small&language=en&enhance=true&split_enhance=true&auto_edit=1")
	if len(frames) != 2 {
		t.Fatalf("want today's raw + enhanced, got %d: %v", len(frames), frames)
	}
}

func autoEditHTTPRequest(t *testing.T, postProcess func(context.Context, string, string, string) (string, string, error), e2e bool) map[string]any {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"translate to spanish"}`)
	}))
	t.Cleanup(backend.Close)
	g := &Gateway{
		backends:     []Backend{{Name: "small", URL: backend.URL, Aliases: []string{"small"}}},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}
	g.health.set("small", true)

	body, ct := buildMultipart(t, map[string]string{"model": "small"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions?enhance=true&auto_edit=1", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	if e2e {
		req.Header.Set("X-Diction-E2E", testE2EClientKey(t))
	}
	rr := httptest.NewRecorder()
	g.TranscriptionHandlerWithPostProcess(postProcess)(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func TestAutoEdit_HTTPSwitchedCarriesFlag(t *testing.T) {
	stubResolveIntent(t, true)
	out := autoEditHTTPRequest(t, recordIntent("Hola."), false)
	if out["mode"] != "edit" || out["auto_edit"] != true || out["text"] != "Hola." {
		t.Errorf("response = %v, want mode=edit auto_edit=true", out)
	}
}

func TestAutoEdit_HTTPSwitchedE2EEnvelopeCarriesFlag(t *testing.T) {
	stubResolveIntent(t, true)
	out := autoEditHTTPRequest(t, recordIntent("Hola."), true)
	if _, ok := out["e2e"]; !ok || out["mode"] != "edit" || out["auto_edit"] != true {
		t.Errorf("E2E response = %v, want envelope with mode=edit and auto_edit outside the ciphertext", out)
	}
}

func TestAutoEdit_HTTPFailedEditIsFailedNotRaw(t *testing.T) {
	stubResolveIntent(t, true)
	failing := func(context.Context, string, string, string) (string, string, error) {
		return "", "", errors.New("llm down")
	}
	out := autoEditHTTPRequest(t, failing, false)
	if out["text"] != "" || out["status"] != "failed" || out["mode"] != "edit" {
		t.Errorf("response = %v, want empty text, status=failed, mode=edit", out)
	}
}

func TestAutoEdit_HTTPKeptHasNoFlag(t *testing.T) {
	stubResolveIntent(t, false)
	out := autoEditHTTPRequest(t, recordIntent("Translate to Spanish."), false)
	if _, ok := out["auto_edit"]; ok {
		t.Errorf("unswitched response carried auto_edit: %v", out)
	}
}

// testE2EClientKey is a fresh X25519 public key, the X-Diction-E2E header value.
func testE2EClientKey(t *testing.T) string {
	t.Helper()
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes())
}
