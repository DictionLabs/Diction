package core

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func installFakeFfmpeg(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

var notWAV = bytes.Repeat([]byte{0xDE, 0xAD}, 64)

func TestConvertToWAVBytes_SlowDecoderIsKilled(t *testing.T) {
	installFakeFfmpeg(t, "exec sleep 30\n")
	t.Setenv("DICTION_DECODE_TIMEOUT_SECONDS", "1")
	resetDecodeLimits()
	t.Cleanup(resetDecodeLimits)

	done := make(chan error, 1)
	start := time.Now()
	go func() { _, err := convertToWAVBytes(notWAV, "audio.m4a"); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("want an error from a decode that hit the ceiling")
		}
		if d := time.Since(start); d > 10*time.Second {
			t.Fatalf("decode took %v; ceiling is 1s", d)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("decode still running after 10s; ceiling is 1s")
	}
}

func TestConvertToWAVBytes_OversizeOutputRejected(t *testing.T) {
	installFakeFfmpeg(t, `for a; do out="$a"; done
head -c 8388608 /dev/zero > "$out"
`)
	t.Setenv("DICTION_DECODE_MAX_SECONDS", "1")
	resetDecodeLimits()
	t.Cleanup(resetDecodeLimits)
	if _, err := convertToWAVBytes(notWAV, "audio.m4a"); err == nil {
		t.Fatal("want an error for decoded output above the cap")
	}
}

func TestConvertToWAVBytes_ConcurrencyBounded(t *testing.T) {
	dir := installFakeFfmpeg(t, `d="$(dirname "$0")"
touch "$d/run.$$"
ls "$d" | grep -c '^run\.' >> "$d/peak"
sleep 1
rm "$d/run.$$"
exit 1
`)
	t.Setenv("DICTION_DECODE_CONCURRENCY", "1")
	resetDecodeLimits()
	t.Cleanup(resetDecodeLimits)

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); convertToWAVBytes(notWAV, "audio.m4a") }()
	}
	wg.Wait()
	raw, _ := os.ReadFile(filepath.Join(dir, "peak"))
	peak := 0
	for _, f := range strings.Fields(string(raw)) {
		if n, _ := strconv.Atoi(f); n > peak {
			peak = n
		}
	}
	if peak != 1 {
		t.Fatalf("peak concurrent decodes = %d (log %q), want 1", peak, raw)
	}
}

func TestTranscriptionHandler_DecodeBusyReturns503(t *testing.T) {
	installFakeFfmpeg(t, "exit 1\n")
	t.Setenv("DICTION_DECODE_CONCURRENCY", "1")
	resetDecodeLimits()
	t.Cleanup(resetDecodeLimits)
	prevWait := decodeQueueWait
	decodeQueueWait = 50 * time.Millisecond
	t.Cleanup(func() { decodeQueueWait = prevWait })

	lim := currentDecodeLimits()
	lim.sem <- struct{}{}
	defer func() { <-lim.sem }()

	backend, hits := countingBackend(t, http.StatusOK, "ok")
	g := &Gateway{
		backends:     []Backend{{Name: "b", URL: backend.URL, Aliases: []string{"b"}, NeedsWAV: true}},
		health:       newHealthState(),
		defaultModel: "b",
		maxBodySize:  10 * 1024 * 1024,
	}
	g.health.set("b", true)
	body, ct := buildMultipart(t, map[string]string{"language": "en"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %q)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"error"`) || rr.Header().Get("Retry-After") == "" {
		t.Errorf("want JSON error body and Retry-After, got %q / %q", rr.Body.String(), rr.Header().Get("Retry-After"))
	}
	if hits.Load() != 0 {
		t.Errorf("backend hit %d times; a busy decode must not reach it", hits.Load())
	}
	if !g.health.get("b") {
		t.Error("a busy decoder must not demote the backend")
	}
}

func TestTranscriptionHandler_RetryDecodeBusyReturns503(t *testing.T) {
	installFakeFfmpeg(t, "exit 1\n")
	t.Setenv("DICTION_DECODE_CONCURRENCY", "1")
	resetDecodeLimits()
	t.Cleanup(resetDecodeLimits)
	prevWait := decodeQueueWait
	decodeQueueWait = 50 * time.Millisecond
	t.Cleanup(func() { decodeQueueWait = prevWait })

	lim := currentDecodeLimits()
	lim.sem <- struct{}{}
	defer func() { <-lim.sem }()

	primary, primaryHits := countingBackend(t, http.StatusInternalServerError, "")
	wavBackend, wavHits := countingBackend(t, http.StatusOK, "ok")
	g := &Gateway{
		backends: []Backend{
			{Name: "primary", URL: primary.URL, Aliases: []string{"primary"}},
			{Name: "wav", URL: wavBackend.URL, Aliases: []string{"wav"}, NeedsWAV: true},
		},
		health:        newHealthState(),
		defaultModel:  "primary",
		fallbackModel: "wav",
		maxBodySize:   10 * 1024 * 1024,
	}
	g.health.set("primary", true)
	g.health.set("wav", true)
	body, ct := buildMultipart(t, map[string]string{"language": "en"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if primaryHits.Load() != 1 {
		t.Fatalf("primary hit %d times, want 1", primaryHits.Load())
	}
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body %q)", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("want Retry-After on the busy 503")
	}
	if wavHits.Load() != 0 {
		t.Errorf("WAV backend hit %d times with an undecoded body", wavHits.Load())
	}
}
