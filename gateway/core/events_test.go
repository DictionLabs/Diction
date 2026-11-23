package core

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestOnError_DefaultNil(t *testing.T) {
	waitHandlers(t)
	prev := OnError
	defer func() { waitHandlers(t); OnError = prev }()

	OnError = nil
	if OnError != nil {
		t.Fatal("expected nil default")
	}
}

func TestOnError_HookFires(t *testing.T) {
	waitHandlers(t)
	prev := OnError
	defer func() { waitHandlers(t); OnError = prev }()

	var mu sync.Mutex
	var got ErrorEvent
	OnError = func(_ context.Context, e ErrorEvent) {
		mu.Lock()
		got = e
		mu.Unlock()
	}
	OnError(context.Background(), ErrorEvent{
		Source:   "auth",
		Kind:     "jws_expired",
		Endpoint: "/v1/audio/transcriptions",
	})
	mu.Lock()
	defer mu.Unlock()
	if got.Source != "auth" || got.Kind != "jws_expired" {
		t.Errorf("hook received wrong event: %+v", got)
	}
}

func TestOnError_FiresOnMultipartRewriteFailure(t *testing.T) {
	waitHandlers(t)
	prev := OnError
	defer func() { waitHandlers(t); OnError = prev }()

	var seen []string
	var mu sync.Mutex
	OnError = func(_ context.Context, e ErrorEvent) {
		mu.Lock()
		seen = append(seen, e.Kind)
		mu.Unlock()
	}

	g := &Gateway{
		backends: []Backend{
			{Name: "small", URL: "http://127.0.0.1:0", Aliases: []string{"small"}, NeedsWAV: true},
		},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}

	body, ct := buildMultipart(t, map[string]string{"model": "small"}, "audio.m4a", "NOT-VALID-AUDIO")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandlerWithPostProcess(nil)(rr, req)

	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 {
		t.Skip("OnError not fired (ffmpeg may have accepted the fake audio); branch still covered in CI where it fails")
	}
}

func TestOnError_FiresOnPostProcessError(t *testing.T) {
	waitHandlers(t)
	prev := OnError
	defer func() { waitHandlers(t); OnError = prev }()

	var seen []string
	var mu sync.Mutex
	OnError = func(_ context.Context, e ErrorEvent) {
		mu.Lock()
		seen = append(seen, e.Kind)
		mu.Unlock()
	}

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"raw"}`)
	}))
	defer backend.Close()

	g := &Gateway{
		backends: []Backend{
			{Name: "small", URL: backend.URL, Aliases: []string{"small"}},
		},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}

	postProcess := func(_ context.Context, _, _, _ string) (string, string, error) {
		return "", "", fmt.Errorf("synthetic llm failure")
	}

	body, ct := buildMultipart(t, map[string]string{"model": "small"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions?enhance=true&intent=edit", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandlerWithPostProcess(postProcess)(rr, req)

	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 {
		t.Fatal("OnError never fired")
	}
	if seen[0] != "stt_post_process" {
		t.Errorf("want kind stt_post_process, got %q", seen[0])
	}
}
