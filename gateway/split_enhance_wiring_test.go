package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Does the handler buildMux builds answer with split frames, end to end through the real mux?

// splitWiringMux builds the production mux against fake STT and LLM backends via env vars.
func splitWiringMux(t *testing.T, transcript, enhanced string) http.Handler {
	t.Helper()

	stt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"text": transcript})
	}))
	t.Cleanup(stt.Close)

	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, enhanced)
	}))
	t.Cleanup(llm.Close)

	t.Setenv("CUSTOM_BACKEND_URL", stt.URL)
	t.Setenv("CUSTOM_BACKEND_MODEL", "custom")
	t.Setenv("DEFAULT_MODEL", "custom")
	t.Setenv("LLM_BASE_URL", llm.URL)
	t.Setenv("LLM_MODEL", "test-model")
	t.Setenv("DICTION_GATEWAY_AUTH", "off")
	t.Setenv("DICTION_KEY_PATH", filepath.Join(t.TempDir(), "keys.json"))

	mux, _, err := buildMux()
	if err != nil {
		t.Fatalf("buildMux: %v", err)
	}
	return mux
}

// streamFrames dials the stream route, sends silence plus done, returns every text frame.
func streamFrames(t *testing.T, mux http.Handler, query string) []string {
	t.Helper()

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/audio/stream?" + query
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	if err := conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200)); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	done, _ := json.Marshal(map[string]string{"action": "done"})
	if err := conn.Write(ctx, websocket.MessageText, done); err != nil {
		t.Fatalf("write done: %v", err)
	}

	var frames []string
	for {
		_, data, readErr := conn.Read(ctx)
		if readErr != nil {
			return frames
		}
		frames = append(frames, string(data))
	}
}

// split_enhance=true must yield two frames (raw, then enhanced) through the real mux.
func TestStreamWiring_SplitEnhanceYieldsRawThenEnhanced(t *testing.T) {
	mux := splitWiringMux(t, "raw transcript here", "enhanced transcript here")
	frames := streamFrames(t, mux, "enhance=true&split_enhance=true")

	if len(frames) != 2 {
		t.Fatalf("expected 2 frames (raw then enhanced), got %d: %q", len(frames), frames)
	}

	var raw struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(frames[0]), &raw); err != nil {
		t.Fatalf("frame 0 not JSON: %v (%s)", err, frames[0])
	}
	if raw.Text != "raw transcript here" {
		t.Errorf("frame 0 must be the RAW transcript, got %q", raw.Text)
	}

	var enh struct {
		Type   string `json:"type"`
		Text   string `json:"text"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(frames[1]), &enh); err != nil {
		t.Fatalf("frame 1 not JSON: %v (%s)", err, frames[1])
	}
	if enh.Type != "enhanced" {
		t.Errorf(`frame 1 must be type "enhanced", got %q`, enh.Type)
	}
	if enh.Status == "failed" {
		t.Fatalf("enhance failed through the real wiring: %s", frames[1])
	}
	if enh.Text != "enhanced transcript here" {
		t.Errorf("frame 1 must carry the enhanced text, got %q", enh.Text)
	}
}

// Without the parameter the answer stays one enhanced frame, so the split is opt-in.
func TestStreamWiring_WithoutSplitParamStaysSingleFrame(t *testing.T) {
	mux := splitWiringMux(t, "raw transcript here", "enhanced transcript here")
	frames := streamFrames(t, mux, "enhance=true")

	if len(frames) != 1 {
		t.Fatalf("expected exactly 1 frame without split_enhance, got %d: %q", len(frames), frames)
	}
	var only struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(frames[0]), &only); err != nil {
		t.Fatalf("frame not JSON: %v (%s)", err, frames[0])
	}
	if only.Text != "enhanced transcript here" {
		t.Errorf("the single frame must already be enhanced, got %q", only.Text)
	}
}
