package core

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseWAVDurationMs_ZeroByteRate(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteWAVHeader(&buf, 32000); err != nil {
		t.Fatal(err)
	}
	buf.Write(make([]byte, 32000))
	data := buf.Bytes()
	binary.LittleEndian.PutUint32(data[24:28], 0)

	got := ParseWAVDurationMs(data)
	if got != 0 {
		t.Errorf("zero byteRate: want 0, got %d", got)
	}
}

func TestParseWAVDurationMs_OddChunkPaddingSkipped(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteWAVHeader(&buf, 32000); err == nil {
		buf.Truncate(36)
	} else {
		t.Fatal(err)
	}
	buf.Write([]byte("LIST"))
	sizeBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(sizeBytes, 1)
	buf.Write(sizeBytes)
	buf.WriteByte(0x00)
	buf.WriteByte(0x00)
	buf.Write([]byte("data"))
	binary.LittleEndian.PutUint32(sizeBytes, 32000)
	buf.Write(sizeBytes)
	buf.Write(make([]byte, 32000))

	got := ParseWAVDurationMs(buf.Bytes())
	if got != 1000 {
		t.Errorf("odd-padded LIST + data: want 1000ms, got %d", got)
	}
}

func TestParseWAVDurationMs_NoDataChunk(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteWAVHeader(&buf, 32000); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	copy(b[36:40], "JUNK")

	got := ParseWAVDurationMs(b)
	if got != 0 {
		t.Errorf("no data chunk: want 0, got %d", got)
	}
}

func TestDecryptRequest_InvalidBase64Ciphertext(t *testing.T) {
	serverPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	clientPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	clientPubB64 := base64.RawURLEncoding.EncodeToString(clientPriv.PublicKey().Bytes())

	_, err := DecryptRequest("not!valid!base64", clientPubB64, serverPriv, "diction-cleanup-req-v1")
	if err == nil {
		t.Fatal("expected decode error for invalid ciphertext base64")
	}
}

func TestTranscriptionHandler_E2EEncryptFailureFallsBackToPlain(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"transcript"}`)
	}))
	defer backend.Close()

	var capturedKind string
	waitHandlers(t)
	OnError = func(_ context.Context, ev ErrorEvent) {
		if ev.Source == "e2e" {
			capturedKind = ev.Kind
		}
	}
	t.Cleanup(func() { waitHandlers(t); OnError = nil })

	g := &Gateway{
		backends: []Backend{
			{Name: "small", URL: backend.URL, Aliases: []string{"small"}},
		},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}

	badPub := base64.RawURLEncoding.EncodeToString([]byte("tooshort"))

	body, ct := buildMultipart(t, map[string]string{}, "audio.m4a", "audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	req.Header.Set("X-Diction-E2E", badPub)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200 (fall-through to plain), got %d body=%s", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); !bytes.Contains([]byte(body), []byte(`"text":"transcript"`)) {
		t.Errorf("body: want plain {\"text\":\"transcript\"}, got %q", body)
	}
	if capturedKind != "e2e_encrypt" {
		t.Errorf("OnError kind: want e2e_encrypt, got %q", capturedKind)
	}
}

func TestTranscriptionHandler_E2EResponseCarriesMode(t *testing.T) {
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

	clientPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	clientPubB64 := base64.RawURLEncoding.EncodeToString(clientPriv.PublicKey().Bytes())

	postProcess := func(_ context.Context, text, _, _ string) (string, string, error) {
		return "polished " + text, "edit", nil
	}

	body, ct := buildMultipart(t, map[string]string{}, "audio.m4a", "audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions?enhance=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	req.Header.Set("X-Diction-E2E", clientPubB64)
	rr := httptest.NewRecorder()
	g.TranscriptionHandlerWithPostProcess(postProcess)(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Body.String(); !bytes.Contains([]byte(got), []byte(`"mode":"edit"`)) {
		t.Errorf("e2e response body: want mode field, got %q", got)
	}
	if got := rr.Body.String(); !bytes.Contains([]byte(got), []byte(`"e2e"`)) {
		t.Errorf("e2e response body: want e2e envelope, got %q", got)
	}
}

func TestTranscriptionHandler_ForwardsAuthHeaderToBackend(t *testing.T) {
	var receivedAuth string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"ok"}`)
	}))
	defer backend.Close()

	g := &Gateway{
		backends: []Backend{
			{Name: "small", URL: backend.URL, Aliases: []string{"small"}, AuthHeader: "Bearer test-token"},
		},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}

	body, ct := buildMultipart(t, map[string]string{}, "audio.m4a", "audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d", rr.Code)
	}
	if receivedAuth != "Bearer test-token" {
		t.Errorf("upstream Authorization: want %q, got %q", "Bearer test-token", receivedAuth)
	}
}

func TestTranscriptionHandler_MalformedMultipartReturns500(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"never reached"}`)
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

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions",
		bytes.NewReader([]byte("this is not a multipart body")))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=xyz")
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: want 500 (multipart parse error), got %d body=%s",
			rr.Code, rr.Body.String())
	}
}
