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

// --- WAV parsing edge cases (wav.go) ---

// TestParseWAVDurationMs_ZeroByteRate covers the `byteRate == 0` early-return
// branch (wav.go:53-55). A WAV header with sampleRate=0 or bitsPerSample=0
// would divide-by-zero; the function must return 0 instead.
func TestParseWAVDurationMs_ZeroByteRate(t *testing.T) {
	// Build a "valid" WAV header but with sampleRate=0 → byteRate=0.
	var buf bytes.Buffer
	if err := WriteWAVHeader(&buf, 32000); err != nil {
		t.Fatal(err)
	}
	buf.Write(make([]byte, 32000))
	data := buf.Bytes()
	// Zero out the sample rate field (bytes 24..28).
	binary.LittleEndian.PutUint32(data[24:28], 0)

	got := ParseWAVDurationMs(data)
	if got != 0 {
		t.Errorf("zero byteRate: want 0, got %d", got)
	}
}

// TestParseWAVDurationMs_OddChunkPaddingSkipped covers the odd-chunk-size
// padding branch (wav.go:65-67). WAV chunks are aligned to 2-byte boundaries;
// an odd-size chunk before the `data` chunk must be skipped correctly.
func TestParseWAVDurationMs_OddChunkPaddingSkipped(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteWAVHeader(&buf, 32000); err == nil {
		// Truncate the header at end of fmt (byte 36) so we can inject a LIST
		// chunk with an odd size before the data chunk.
		buf.Truncate(36)
	} else {
		t.Fatal(err)
	}
	// LIST chunk with size=1 (odd) — 1 byte payload + 1 pad byte
	buf.Write([]byte("LIST"))
	sizeBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(sizeBytes, 1)
	buf.Write(sizeBytes)
	buf.WriteByte(0x00) // payload
	buf.WriteByte(0x00) // pad
	// data chunk: 32000 bytes = 1 second
	buf.Write([]byte("data"))
	binary.LittleEndian.PutUint32(sizeBytes, 32000)
	buf.Write(sizeBytes)
	buf.Write(make([]byte, 32000))

	got := ParseWAVDurationMs(buf.Bytes())
	if got != 1000 {
		t.Errorf("odd-padded LIST + data: want 1000ms, got %d", got)
	}
}

// TestParseWAVDurationMs_NoDataChunk covers the loop-exit fallthrough
// (wav.go:69): a valid header with no `data` chunk returns 0.
func TestParseWAVDurationMs_NoDataChunk(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteWAVHeader(&buf, 32000); err != nil {
		t.Fatal(err)
	}
	// Overwrite the "data" chunk ID (bytes 36..40) with something else so the
	// scan reaches EOF without finding data.
	b := buf.Bytes()
	copy(b[36:40], "JUNK")

	got := ParseWAVDurationMs(b)
	if got != 0 {
		t.Errorf("no data chunk: want 0, got %d", got)
	}
}

// --- DecryptRequest edge cases (e2e.go) ---

// TestDecryptRequest_InvalidBase64Ciphertext covers the base64.DecodeString
// error branch for ctB64 (e2e.go:96-98). A ciphertext with characters
// outside the RawURL base64 alphabet must fail decode cleanly.
func TestDecryptRequest_InvalidBase64Ciphertext(t *testing.T) {
	serverPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	clientPriv, _ := ecdh.X25519().GenerateKey(rand.Reader)
	clientPubB64 := base64.RawURLEncoding.EncodeToString(clientPriv.PublicKey().Bytes())

	// '!' is invalid in RawURL base64 alphabet.
	_, err := DecryptRequest("not!valid!base64", clientPubB64, serverPriv, "diction-cleanup-req-v1")
	if err == nil {
		t.Fatal("expected decode error for invalid ciphertext base64")
	}
}

// --- e2e encrypt failure fallback (proxy.go writeTranscriptionResponse) ---

// TestTranscriptionHandler_E2EEncryptFailureFallsBackToPlain covers the e2e
// encrypt-failure branch in writeTranscriptionResponse (proxy.go:300-311).
// When X-Diction-E2E carries a syntactically valid base64 pubkey that is not
// a valid X25519 point (wrong length), EncryptTranscript fails and the
// handler must fall through to plain JSON instead of returning an error,
// firing the OnError callback with source=e2e / kind=e2e_encrypt.
func TestTranscriptionHandler_E2EEncryptFailureFallsBackToPlain(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"transcript"}`)
	}))
	defer backend.Close()

	// Capture OnError callbacks — the e2e_encrypt failure must fire one.
	var capturedKind string
	OnError = func(_ context.Context, ev ErrorEvent) {
		if ev.Source == "e2e" {
			capturedKind = ev.Kind
		}
	}
	t.Cleanup(func() { OnError = nil })

	g := &Gateway{
		backends: []Backend{
			{Name: "small", URL: backend.URL, Aliases: []string{"small"}},
		},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}

	// Valid base64, invalid X25519 pubkey (only 8 bytes; X25519 needs 32).
	badPub := base64.RawURLEncoding.EncodeToString([]byte("tooshort"))

	body, ct := buildMultipart(t, map[string]string{}, "audio.m4a", "audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	req.Header.Set("X-Diction-E2E", badPub)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	// Client sees a plain JSON transcript (not an error), the e2e failure was
	// swallowed and reported via OnError.
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

// --- rewriteMultipart on malformed multipart body (proxy.go:90-92) ---

// TestTranscriptionHandler_E2EResponseCarriesMode covers the `if mode != ""`
// branch inside the e2e-success path of writeTranscriptionResponse
// (proxy.go:291-293). When postProcess returns a non-empty mode alongside a
// valid X-Diction-E2E pubkey, the encrypted envelope must include the mode.
func TestTranscriptionHandler_E2EResponseCarriesMode(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

// TestTranscriptionHandler_ForwardsAuthHeaderToBackend covers the
// backend.AuthHeader forwarding branch (proxy.go:487-489). Backends with a
// non-empty AuthHeader must receive it on the upstream request.
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

// TestTranscriptionHandler_MalformedMultipartReturns500 covers the
// `read multipart` error branch (proxy.go:90-92). When the client sends a
// Content-Type: multipart/... with a body that doesn't match the declared
// boundary, rewriteMultipart returns an error and the handler responds 500.
func TestTranscriptionHandler_MalformedMultipartReturns500(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	// Body declares boundary=xyz but contains no matching parts — NextPart
	// will return a non-EOF error on the first read.
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
