package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A self-hoster may set the post-delivery budget (DICTION_LIVE_ENHANCE_TIMEOUT_MS) above
// the stream idle watchdog, for a slow local LLM. The enhanced frame must still arrive:
// the watchdog bounds the gap between *client* frames, and the client sends nothing
// after `done`. (.claude/plans/insert-once-plan.md, C1.)
func TestSplitEnhancedFrameOutlivesIdleWatchdog(t *testing.T) {
	whisper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"text":"hello there world friend"}`)
	}))
	t.Cleanup(whisper.Close)

	g := &Gateway{
		backends:          []Backend{{Name: "small", URL: whisper.URL, Aliases: []string{"small"}}},
		health:            newHealthState(),
		defaultModel:      "small",
		maxBodySize:       10 * 1024 * 1024,
		streamIdleTimeout: 300 * time.Millisecond,
	}
	g.health.set("small", true)

	slowLLM := func(ctx context.Context, _, _, _ string) (string, string, error) {
		select {
		case <-time.After(900 * time.Millisecond): // three idle windows
			return "Hello there, world friend.", "transcribe", nil
		case <-ctx.Done():
			return "", "", ctx.Err()
		}
	}
	srv := httptest.NewServer(g.StreamingHandlerWithSplitEnhance(slowLLM, slowLLM))
	t.Cleanup(srv.Close)

	frames := dialAndFinish(t, srv, "model=small&language=en&enhance=true&split_enhance=true")
	if len(frames) != 2 {
		t.Fatalf("want raw + enhanced frames, got %d: %v", len(frames), frames)
	}
	var enhanced EnhancedFrame
	_ = json.Unmarshal([]byte(frames[1]), &enhanced)
	if enhanced.Status == "failed" || enhanced.Text != "Hello there, world friend." {
		t.Errorf("enhanced frame = %+v, want the slow LLM's text", enhanced)
	}
}
