package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type outcomeEvent struct{ Source, Kind, Reason, Hint string }

type outcomeObserved struct {
	HTTPStatus  int
	CloseCode   websocket.StatusCode
	CloseReason string
	Text        string
	Events      []outcomeEvent
	Failed      []string
}

type outcomeCase struct {
	name   string
	setup  func(t *testing.T) http.Handler
	query  string
	opus   bool
	client func(ctx context.Context, conn *websocket.Conn)
	want   outcomeObserved
}

func sendDone(ctx context.Context, conn *websocket.Conn) {
	done, _ := json.Marshal(map[string]string{"action": "done"})
	_ = conn.Write(ctx, websocket.MessageText, done)
}

func runOutcomeCase(t *testing.T, c outcomeCase) outcomeObserved {
	t.Helper()
	evRec, restoreEv := withCapturedOnError(t)
	failRec, restoreFail := withCapturedOnRequestFailed(t)
	srv := httptest.NewServer(trackHandler(c.setup(t)))
	defer srv.Close()

	var got outcomeObserved
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var opts *websocket.DialOptions
	if c.opus {
		opts = &websocket.DialOptions{Subprotocols: []string{opusSubprotocol}}
	}
	conn, resp, err := websocket.Dial(ctx, wsURL(srv, c.query), opts)
	if err != nil {
		if resp == nil {
			t.Fatalf("dial: %v", err)
		}
		got.HTTPStatus = resp.StatusCode
	} else {
		c.client(ctx, conn)
		for {
			typ, data, rerr := conn.Read(ctx)
			if rerr != nil {
				var ce websocket.CloseError
				if errors.As(rerr, &ce) {
					got.CloseCode, got.CloseReason = ce.Code, ce.Reason
				}
				break
			}
			if typ == websocket.MessageText {
				got.Text = string(data)
			}
		}
		conn.CloseNow()
	}
	restoreEv()
	restoreFail()
	for _, e := range evRec.All() {
		got.Events = append(got.Events, outcomeEvent{e.Source, e.Kind, e.Reason, e.Hint})
	}
	got.Failed = failRec.All()
	return got
}

func TestStreamingOutcome_EveryTerminalExit(t *testing.T) {
	pcm := make([]byte, 3200)
	plain := func(status int, text string) func(t *testing.T) http.Handler {
		return func(t *testing.T) http.Handler {
			srv, g := startStreamingServer(t, text, status)
			srv.Close()
			return g.StreamingHandler()
		}
	}
	cases := []outcomeCase{
		{
			name: "route_fail",
			setup: func(t *testing.T) http.Handler {
				_, g := startStreamingServer(t, "ok", http.StatusOK)
				g.health.set("small", false)
				return g.StreamingHandler()
			},
			want: outcomeObserved{HTTPStatus: 503,
				Events: []outcomeEvent{{"stt", "stt_route_fail", "", "no healthy backend for this language"}},
				Failed: []string{errTypeSTTError}},
		},
		{
			name: "opus_unavailable",
			setup: func(t *testing.T) http.Handler {
				orig := lookupFfmpeg()
				testSetFfmpegPath("")
				t.Cleanup(func() { testSetFfmpegPath(orig) })
				return makeGateway(echoTranscript(t, "x").URL, true, 10<<20).StreamingHandler()
			},
			query:  "codec=opus",
			client: func(context.Context, *websocket.Conn) {},
			want: outcomeObserved{CloseCode: wsCloseFailed, CloseReason: "opus unavailable: ffmpeg not in PATH",
				Events: []outcomeEvent{{"streaming", "opus_decode", "", "ffmpeg not found; cannot decode Opus for this backend"}},
				Failed: []string{errTypeSTTError}},
		},
		{
			name: "compressed_too_large",
			setup: func(t *testing.T) http.Handler {
				return makeGateway(echoTranscript(t, "x").URL, false, 100).StreamingHandler()
			},
			client: func(ctx context.Context, c *websocket.Conn) {
				_ = c.Write(ctx, websocket.MessageBinary, make([]byte, 200))
			},
			want: outcomeObserved{CloseCode: wsCloseTooLarge, CloseReason: "audio too large",
				Events: []outcomeEvent{{"streaming", "ws_audio_too_large", "compressed", "received audio exceeded maxPCM before decode"}},
				Failed: []string{errTypeSTTError}},
		},
		{
			name: "unknown_container",
			setup: func(t *testing.T) http.Handler {
				return makeGateway(echoTranscript(t, "x").URL, false, 10<<20).StreamingHandler()
			},
			query: "codec=opus",
			opus:  true,
			client: func(ctx context.Context, c *websocket.Conn) {
				_ = c.Write(ctx, websocket.MessageBinary, []byte("junkjunk"))
			},
			want: outcomeObserved{CloseCode: wsCloseUnknown, CloseReason: "expected Ogg or WebM container",
				Events: []outcomeEvent{{"streaming", "opus_decode", "", "expected Ogg or WebM container; got unknown magic bytes"}},
				Failed: []string{errTypeSTTError}},
		},
		{
			name: "ffmpeg_start_failed",
			setup: func(t *testing.T) http.Handler {
				orig := lookupFfmpeg()
				testSetFfmpegPath("/nonexistent/ffmpeg")
				t.Cleanup(func() { testSetFfmpegPath(orig) })
				return makeGateway(echoTranscript(t, "x").URL, true, 10<<20).StreamingHandler()
			},
			query: "codec=opus",
			opus:  true,
			client: func(ctx context.Context, c *websocket.Conn) {
				_ = c.Write(ctx, websocket.MessageBinary, []byte("OggS...."))
			},
			want: outcomeObserved{CloseCode: websocket.StatusInternalError, CloseReason: "ffmpeg error",
				Events: []outcomeEvent{{"streaming", "opus_decode", "", "ffmpeg start failed"}},
				Failed: []string{errTypeSTTError}},
		},
		{
			name:   "no_audio",
			setup:  plain(http.StatusOK, "ok"),
			client: sendDone,
			want: outcomeObserved{CloseCode: wsCloseNoAudio, CloseReason: "no audio received",
				Events: []outcomeEvent{{"streaming", "ws_no_audio", "", "socket closed with zero audio bytes"}},
				Failed: []string{errTypeNoAudio}},
		},
		{
			name: "idle_timeout",
			setup: func(t *testing.T) http.Handler {
				_, g := startStreamingServer(t, "ok", http.StatusOK)
				g.streamIdleTimeout = 100 * time.Millisecond
				return g.StreamingHandler()
			},
			client: func(context.Context, *websocket.Conn) {},
			want: outcomeObserved{CloseCode: websocket.StatusPolicyViolation, CloseReason: "idle_timeout",
				Events: []outcomeEvent{{"streaming", "ws_read", wsReasonIdleTimeout, "websocket read failed: " + wsReasonIdleTimeout}},
				Failed: []string{errTypeClientAbort}},
		},
		{
			name:  "success",
			setup: plain(http.StatusOK, "hello"),
			client: func(ctx context.Context, c *websocket.Conn) {
				_ = c.Write(ctx, websocket.MessageBinary, pcm)
				sendDone(ctx, c)
			},
			want: outcomeObserved{CloseCode: websocket.StatusNormalClosure, Text: `{"text":"hello"}`},
		},
		{
			name: "edit_post_process_failed",
			setup: func(t *testing.T) http.Handler {
				_, g := startStreamingServer(t, "make it shorter", http.StatusOK)
				return g.StreamingHandlerWithPostProcess(func(context.Context, string, string, string) (string, string, error) {
					return "", "", fmt.Errorf("llm down")
				})
			},
			query: "enhance=true&intent=edit",
			client: func(ctx context.Context, c *websocket.Conn) {
				_ = c.Write(ctx, websocket.MessageBinary, pcm)
				sendDone(ctx, c)
			},
			want: outcomeObserved{CloseCode: websocket.StatusNormalClosure,
				Text:   `{"text":"","mode":"edit","status":"failed"}`,
				Events: []outcomeEvent{{"stt", "stt_post_process", "", "edit intent post-process failed; returning status=failed"}}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := runOutcomeCase(t, c)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("observed\n  %+v\nwant\n  %+v", got, c.want)
			}
		})
	}
}

func TestStreamingOutcome_TranscriptionFailed(t *testing.T) {
	got := runOutcomeCase(t, outcomeCase{
		setup: func(t *testing.T) http.Handler {
			_, g := startStreamingServer(t, "", http.StatusInternalServerError)
			return g.StreamingHandler()
		},
		client: func(ctx context.Context, c *websocket.Conn) {
			_ = c.Write(ctx, websocket.MessageBinary, make([]byte, 3200))
			sendDone(ctx, c)
		},
	})
	if got.CloseCode != wsCloseFailed || got.CloseReason != "transcription failed" {
		t.Errorf("close = %d %q", got.CloseCode, got.CloseReason)
	}
	if !reflect.DeepEqual(got.Failed, []string{errTypeSTTError}) {
		t.Errorf("failed = %v", got.Failed)
	}
	if len(got.Events) != 1 || !strings.HasPrefix(got.Events[0].Kind, "stt_") {
		t.Errorf("events = %+v", got.Events)
	}
}

func TestStreamingOutcome_ClientDrop(t *testing.T) {
	got := runOutcomeCase(t, outcomeCase{
		setup: func(t *testing.T) http.Handler {
			_, g := startStreamingServer(t, "ok", http.StatusOK)
			return g.StreamingHandler()
		},
		client: func(ctx context.Context, c *websocket.Conn) {
			_ = c.Write(ctx, websocket.MessageBinary, make([]byte, 3200))
			c.CloseNow()
		},
	})
	if len(got.Events) != 1 || got.Events[0].Kind != "ws_read" || got.Events[0].Source != "streaming" {
		t.Errorf("events = %+v", got.Events)
	}
	if !reflect.DeepEqual(got.Failed, []string{errTypeClientAbort}) {
		t.Errorf("failed = %v", got.Failed)
	}
}
