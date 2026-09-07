package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// The split answer's contract, in one place:
//
//	raw frame  → always first, transcribe intents only
//	enhanced   → always exactly one, whatever happens to the LLM
//
// A client that opted in must never be left waiting for a frame that cannot come,
// so every failure path is asserted to still produce status="failed".

// startSplitServer builds a streaming server whose enhance closure is supplied by the
// test. `postDelivery` may be nil to exercise the community-build wiring.
func startSplitServer(
	t *testing.T,
	transcript string,
	inline, postDelivery func(context.Context, string, string, string) (string, string, error),
) *httptest.Server {
	t.Helper()

	whisper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"text":"%s"}`, transcript)
	}))
	t.Cleanup(whisper.Close)

	g := &Gateway{
		backends:     []Backend{{Name: "small", URL: whisper.URL, Aliases: []string{"small"}}},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}
	g.health.set("small", true)

	srv := httptest.NewServer(g.StreamingHandlerWithSplitEnhance(inline, postDelivery))
	t.Cleanup(srv.Close)
	return srv
}

// dialAndFinish opens the socket, sends silence + done, and returns every text frame
// the server wrote before closing.
func dialAndFinish(t *testing.T, srv *httptest.Server, query string) []string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL(srv, query), nil)
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

func echoEnhance(text string) func(context.Context, string, string, string) (string, string, error) {
	return func(context.Context, string, string, string) (string, string, error) {
		return text, "transcribe", nil
	}
}

func TestSplitEnhance_RawThenEnhanced(t *testing.T) {
	srv := startSplitServer(t, "hello there world friend",
		echoEnhance("unused inline"), echoEnhance("Hello there, world friend."))

	frames := dialAndFinish(t, srv, "model=small&language=en&enhance=true&split_enhance=true")
	if len(frames) != 2 {
		t.Fatalf("want 2 frames (raw, enhanced), got %d: %v", len(frames), frames)
	}

	var raw streamResult
	if err := json.Unmarshal([]byte(frames[0]), &raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	if raw.Text != "hello there world friend" {
		t.Errorf("raw frame text = %q, want the untouched transcript", raw.Text)
	}

	var enhanced EnhancedFrame
	if err := json.Unmarshal([]byte(frames[1]), &enhanced); err != nil {
		t.Fatalf("decode enhanced: %v", err)
	}
	if enhanced.Type != "enhanced" || enhanced.Text != "Hello there, world friend." {
		t.Errorf("enhanced frame = %+v, want the post-delivery result", enhanced)
	}
}

// The post-delivery budget must be the one that runs: it is the whole point of the
// split. Passing distinguishable closures is the only way to observe which was called.
func TestSplitEnhance_UsesPostDeliveryClosure(t *testing.T) {
	srv := startSplitServer(t, "one two three four five six seven eight",
		echoEnhance("INLINE"), echoEnhance("POST DELIVERY BUDGET RAN HERE OK"))

	frames := dialAndFinish(t, srv, "model=small&language=en&enhance=true&split_enhance=true")
	if len(frames) != 2 {
		t.Fatalf("want 2 frames, got %d: %v", len(frames), frames)
	}
	var enhanced EnhancedFrame
	_ = json.Unmarshal([]byte(frames[1]), &enhanced)
	if enhanced.Text == "INLINE" {
		t.Error("split session ran the inline closure; must use the post-delivery budget")
	}
}

// Community build: one closure, nil post-delivery. The split still works.
func TestSplitEnhance_NilPostDeliveryFallsBackToInline(t *testing.T) {
	srv := startSplitServer(t, "one two three four five six seven eight",
		echoEnhance("One, two, three, four, five, six, seven, eight."), nil)

	frames := dialAndFinish(t, srv, "model=small&language=en&enhance=true&split_enhance=true")
	if len(frames) != 2 {
		t.Fatalf("want 2 frames, got %d: %v", len(frames), frames)
	}
	var enhanced EnhancedFrame
	_ = json.Unmarshal([]byte(frames[1]), &enhanced)
	if enhanced.Status == "failed" {
		t.Error("nil post-delivery must fall back to inline, not fail")
	}
}

// Back-compat: without the opt-in the response must be byte-identical to today —
// one frame, already enhanced.
func TestSplitEnhance_AbsentParamKeepsSingleFrame(t *testing.T) {
	srv := startSplitServer(t, "hello there world friend",
		echoEnhance("Hello there, world friend."), echoEnhance("SPLIT"))

	frames := dialAndFinish(t, srv, "model=small&language=en&enhance=true")
	if len(frames) != 1 {
		t.Fatalf("want exactly 1 frame without split_enhance, got %d: %v", len(frames), frames)
	}
	var only streamResult
	_ = json.Unmarshal([]byte(frames[0]), &only)
	if only.Text != "Hello there, world friend." {
		t.Errorf("legacy frame = %q, want the enhanced text", only.Text)
	}
}

// The destructive-regression guard. On an edit intent the transcript is the user's
// spoken *instruction*; emitting it as a raw text frame would type "make this formal"
// into their document. There must be no raw frame, split_enhance or not.
func TestSplitEnhance_EditIntentNeverEmitsRawFrame(t *testing.T) {
	failing := func(context.Context, string, string, string) (string, string, error) {
		return "", "", errors.New("llm down")
	}
	for _, intent := range []string{"edit", "edit-selected"} {
		t.Run(intent, func(t *testing.T) {
			srv := startSplitServer(t, "make this more formal", failing, failing)
			frames := dialAndFinish(t, srv,
				"model=small&language=en&enhance=true&split_enhance=true&intent="+intent)
			if len(frames) != 1 {
				t.Fatalf("want 1 frame on edit intent, got %d: %v", len(frames), frames)
			}
			var res streamResult
			_ = json.Unmarshal([]byte(frames[0]), &res)
			if res.Text != "" {
				t.Errorf("edit intent leaked the spoken instruction as text: %q", res.Text)
			}
			if res.Status != "failed" {
				t.Errorf("status = %q, want failed", res.Status)
			}
		})
	}
}

func TestSplitEnhance_LLMErrorStillSendsFailedFrame(t *testing.T) {
	failing := func(context.Context, string, string, string) (string, string, error) {
		return "", "", errors.New("llm down")
	}
	srv := startSplitServer(t, "hello there world friend", failing, failing)

	frames := dialAndFinish(t, srv, "model=small&language=en&enhance=true&split_enhance=true")
	if len(frames) != 2 {
		t.Fatalf("want raw + failed frames, got %d: %v", len(frames), frames)
	}
	var enhanced EnhancedFrame
	_ = json.Unmarshal([]byte(frames[1]), &enhanced)
	if enhanced.Status != "failed" || enhanced.Text != "" {
		t.Errorf("enhanced frame = %+v, want status=failed with no text", enhanced)
	}
}

// A closure that outlives its own deadline behaves like any other error: the client
// still gets its one enhanced frame.
func TestSplitEnhance_TimeoutStillSendsFailedFrame(t *testing.T) {
	slow := func(ctx context.Context, _, _, _ string) (string, string, error) {
		deadlined, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		defer cancel()
		<-deadlined.Done()
		return "", "", deadlined.Err()
	}
	srv := startSplitServer(t, "hello there world friend", slow, slow)

	frames := dialAndFinish(t, srv, "model=small&language=en&enhance=true&split_enhance=true")
	if len(frames) != 2 {
		t.Fatalf("want raw + failed frames, got %d: %v", len(frames), frames)
	}
	var enhanced EnhancedFrame
	_ = json.Unmarshal([]byte(frames[1]), &enhanced)
	if enhanced.Status != "failed" {
		t.Errorf("enhanced frame = %+v, want status=failed after timeout", enhanced)
	}
}

// The deletion guard is shared with the realtime socket. An enhancement that drops
// most of the words must be rejected, leaving the user with the raw text they can see.
func TestSplitEnhance_DeletionGuardRejectsTruncation(t *testing.T) {
	srv := startSplitServer(t, "one two three four five six seven eight nine ten",
		echoEnhance("one two"), echoEnhance("one two"))

	frames := dialAndFinish(t, srv, "model=small&language=en&enhance=true&split_enhance=true")
	if len(frames) != 2 {
		t.Fatalf("want raw + failed frames, got %d: %v", len(frames), frames)
	}
	var enhanced EnhancedFrame
	_ = json.Unmarshal([]byte(frames[1]), &enhanced)
	if enhanced.Status != "failed" {
		t.Errorf("enhanced frame = %+v, want the deletion guard to reject it", enhanced)
	}
}

// Influx must not double-count a split session: one dictation, one `requests` row.
func TestSplitEnhance_ReportsTranscriptionExactlyOnce(t *testing.T) {
	whisper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"text":"hello there world friend"}`)
	}))
	defer whisper.Close()

	g := &Gateway{
		backends:     []Backend{{Name: "small", URL: whisper.URL, Aliases: []string{"small"}}},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}
	g.health.set("small", true)

	var calls atomic.Int32
	g.OnTranscription = func(context.Context, string, int64, int, int64, bool, bool) {
		calls.Add(1)
	}

	srv := httptest.NewServer(g.StreamingHandlerWithSplitEnhance(
		echoEnhance("Hello there, world friend."), echoEnhance("Hello there, world friend.")))
	defer srv.Close()

	dialAndFinish(t, srv, "model=small&language=en&enhance=true&split_enhance=true")

	if got := calls.Load(); got != 1 {
		t.Errorf("OnTranscription called %d times, want exactly 1", got)
	}
}
