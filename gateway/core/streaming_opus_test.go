package core

// Gateway Opus streaming test suite.
//
// Covers: codec validation, subprotocol negotiation, passthrough, ffmpeg
// decode path, decoded-size cap, garbage-bytes contract violation,
// granulepos duration, WAV-header/ffmpeg-arg agreement, and benchmark.
//
// Tests that require actual ffmpeg skip when the binary is absent.

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// --- test helpers ---

// ensureFfmpeg skips the test if ffmpeg is not in PATH and caches its path for handler tests.
func ensureFfmpeg(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not in PATH; skipping Opus test")
	}
	testSetFfmpegPath(p)
	return p
}

// genOggOpus generates ~100 ms of silent Ogg/Opus using ffmpeg.
func genOggOpus(t *testing.T) []byte {
	t.Helper()
	p := ensureFfmpeg(t)
	cmd := exec.Command(p, "-y",
		"-f", "lavfi", "-i", "anullsrc=r=48000:cl=mono",
		"-t", "0.1",
		"-c:a", "libopus",
		"-f", "ogg",
		"pipe:1",
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		t.Skipf("ffmpeg libopus encode failed (libopus not compiled in?): %v", err)
	}
	if out.Len() == 0 {
		t.Fatal("ffmpeg produced empty Ogg/Opus output")
	}
	return out.Bytes()
}

// makeGateway builds a Gateway wired to the given backend server.
func makeGateway(backendURL string, needsWAV bool, maxBodySize int64) *Gateway {
	g := &Gateway{
		backends: []Backend{{
			Name:     "test",
			URL:      backendURL,
			Aliases:  []string{"test"},
			NeedsWAV: needsWAV,
		}},
		health:       newHealthState(),
		defaultModel: "test",
		maxBodySize:  maxBodySize,
	}
	g.health.set("test", true)
	return g
}

// echoTranscript is a backend that always returns the given transcript text.
func echoTranscript(t *testing.T, text string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"text":%q}`, text)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// captureBackend records the filename and body from the multipart upload.
type captureBackend struct {
	srv      *httptest.Server
	filename atomic.Value
	body     []byte
	mu       sync.Mutex
}

func newCaptureBackend(t *testing.T, respond string) *captureBackend {
	t.Helper()
	cb := &captureBackend{}
	cb.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseMultipartForm(50 << 20) //nolint:errcheck
		if f, fh, err := r.FormFile("file"); err == nil {
			cb.filename.Store(fh.Filename)
			data, _ := io.ReadAll(f)
			cb.mu.Lock()
			cb.body = data
			cb.mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"text":%q}`, respond)
	}))
	t.Cleanup(cb.srv.Close)
	return cb
}

// streamOpusBinary dials with codec=opus (and optionally the Opus subprotocol),
// sends all ogg bytes as one binary frame, sends done, and reads back the
// transcript. Returns the transcript and the close error (if any).
func streamOpusBinary(t *testing.T, srv *httptest.Server, ogg []byte, offerSub bool) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	opts := &websocket.DialOptions{}
	if offerSub {
		opts.Subprotocols = []string{opusSubprotocol}
	}
	conn, _, err := websocket.Dial(ctx, wsURL(srv, "codec=opus"), opts)
	if err != nil {
		return "", fmt.Errorf("dial: %w", err)
	}
	defer conn.CloseNow()

	if err := conn.Write(ctx, websocket.MessageBinary, ogg); err != nil {
		return "", fmt.Errorf("write ogg: %w", err)
	}
	done, _ := json.Marshal(map[string]string{"action": "done"})
	if err := conn.Write(ctx, websocket.MessageText, done); err != nil {
		return "", fmt.Errorf("write done: %w", err)
	}

	_, data, err := conn.Read(ctx)
	if err != nil {
		return "", err
	}
	var result struct{ Text string }
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("unmarshal: %w", err)
	}
	return result.Text, nil
}

// assertCloseCode reads the connection expecting it to be closed with the given code.
func assertCloseCode(t *testing.T, conn *websocket.Conn, want websocket.StatusCode) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _, err := conn.Read(ctx)
	if err == nil {
		t.Fatal("expected connection close, got successful read")
		return
	}
	var ce websocket.CloseError
	if !errors.As(err, &ce) {
		t.Fatalf("expected CloseError, got %T: %v", err, err)
		return
	}
	if ce.Code != want {
		t.Errorf("close code: want %d, got %d (%s)", want, ce.Code, ce.Reason)
	}
}

// --- codec validation ---

func TestOpus_UnknownCodecRejected400(t *testing.T) {
	b := echoTranscript(t, "nope")
	g := makeGateway(b.URL, false, 10<<20)
	srv := httptest.NewServer(g.StreamingHandler())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/v1/audio/stream?codec=flac")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", resp.StatusCode)
	}
}

// TestOpus_CodecPCMRegression guards that codec=pcm and absent codec are
// still accepted and produce transcripts (no Opus code path invoked).
func TestOpus_CodecPCMRegression(t *testing.T) {
	for _, q := range []string{"", "codec=pcm"} {
		q := q
		t.Run(q, func(t *testing.T) {
			b := echoTranscript(t, "pcm ok")
			g := makeGateway(b.URL, false, 10<<20)
			srv := httptest.NewServer(g.StreamingHandler())
			t.Cleanup(srv.Close)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			conn, _, err := websocket.Dial(ctx, wsURL(srv, q), nil)
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer conn.CloseNow()

			conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200)) //nolint:errcheck
			done, _ := json.Marshal(map[string]string{"action": "done"})
			conn.Write(ctx, websocket.MessageText, done) //nolint:errcheck

			_, data, err := conn.Read(ctx)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			var result struct{ Text string }
			json.Unmarshal(data, &result) //nolint:errcheck
			if result.Text != "pcm ok" {
				t.Errorf("text: want 'pcm ok', got %q", result.Text)
			}
		})
	}
}

// --- passthrough path (NeedsWAV=false) ---

func TestOpus_PassthroughByteIdentical(t *testing.T) {
	ogg := genOggOpus(t)
	cb := newCaptureBackend(t, "passthrough")
	g := makeGateway(cb.srv.URL, false, 10<<20)
	srv := httptest.NewServer(g.StreamingHandler())
	t.Cleanup(srv.Close)

	text, err := streamOpusBinary(t, srv, ogg, true)
	if err != nil {
		t.Fatalf("streamOpusBinary: %v", err)
	}
	if text != "passthrough" {
		t.Errorf("text: want 'passthrough', got %q", text)
	}
	fn, _ := cb.filename.Load().(string)
	if fn != "audio.ogg" {
		t.Errorf("filename: want 'audio.ogg', got %q", fn)
	}
	cb.mu.Lock()
	bodyEq := bytes.Equal(cb.body, ogg)
	bodyLen := len(cb.body)
	cb.mu.Unlock()
	if !bodyEq {
		t.Errorf("body mismatch: sent %d bytes, received %d bytes", len(ogg), bodyLen)
	}
}

// TestOpus_LargeSingleFrameAccepted is the regression test for the
// coder/websocket 32 KiB default read limit. Our own client never hit it (PCM
// chunks are ~682 B), but any batching or whole-file client did, and got closed
// with 1009 "read limited at 32769 bytes" instead of a transcript. Compression
// actively encourages larger, less frequent frames, so this must stay fixed.
func TestOpus_LargeSingleFrameAccepted(t *testing.T) {
	// Build a synthetic Ogg stream comfortably above the old 32 KiB default.
	ogg := buildLargeOggOpus(64 << 10)
	if len(ogg) <= 32<<10 {
		t.Fatalf("fixture too small to exercise the limit: %d bytes", len(ogg))
	}

	cb := newCaptureBackend(t, "large-frame")
	g := makeGateway(cb.srv.URL, false, 10<<20) // NeedsWAV=false → passthrough, no ffmpeg needed
	srv := httptest.NewServer(g.StreamingHandler())
	t.Cleanup(srv.Close)

	text, err := streamOpusBinary(t, srv, ogg, true)
	if err != nil {
		t.Fatalf("large single frame rejected: %v", err)
	}
	if text != "large-frame" {
		t.Errorf("text: want 'large-frame', got %q", text)
	}

	cb.mu.Lock()
	bodyEq := bytes.Equal(cb.body, ogg)
	bodyLen := len(cb.body)
	cb.mu.Unlock()
	if !bodyEq {
		t.Errorf("body mismatch: sent %d bytes, backend received %d bytes", len(ogg), bodyLen)
	}
}

// buildLargeOggOpus returns a syntactically valid Ogg/Opus stream of at least
// minBytes, built from many small pages (writeOggPage carries one ≤255-byte
// segment per page).
func buildLargeOggOpus(minBytes int) []byte {
	var buf bytes.Buffer

	var head [19]byte
	copy(head[:8], "OpusHead")
	head[8] = 1 // version
	head[9] = 1 // mono
	binary.LittleEndian.PutUint32(head[12:], 48000)
	writeOggPage(&buf, 0x02, 0, 0, 0, head[:])

	payload := bytes.Repeat([]byte("x"), 200)
	var seq uint32 = 1
	var granule int64
	for buf.Len() < minBytes {
		granule += 960
		writeOggPage(&buf, 0x00, granule, seq, 0, payload)
		seq++
	}
	return buf.Bytes()
}

// --- ffmpeg decode path (NeedsWAV=true) ---

func TestOpus_FFmpegDecodeProducesWAV(t *testing.T) {
	ogg := genOggOpus(t)
	cb := newCaptureBackend(t, "decoded")
	g := makeGateway(cb.srv.URL, true, 10<<20)
	srv := httptest.NewServer(g.StreamingHandler())
	t.Cleanup(srv.Close)

	text, err := streamOpusBinary(t, srv, ogg, true)
	if err != nil {
		t.Fatalf("streamOpusBinary: %v", err)
	}
	if text != "decoded" {
		t.Errorf("text: want 'decoded', got %q", text)
	}
	fn, _ := cb.filename.Load().(string)
	if fn != "audio.wav" {
		t.Errorf("filename: want 'audio.wav', got %q", fn)
	}
	cb.mu.Lock()
	body := cb.body
	cb.mu.Unlock()
	if len(body) < 44 {
		t.Fatalf("received body too short for WAV (%d bytes)", len(body))
	}
	if string(body[:4]) != "RIFF" || string(body[8:12]) != "WAVE" {
		t.Errorf("received body is not WAV (magic: %q %q)", body[:4], body[8:12])
	}
}

// --- decoded-side size cap ---

func TestOpus_DecodedSizeCapEnforced(t *testing.T) {
	ogg := genOggOpus(t)
	b := echoTranscript(t, "nope")
	g := makeGateway(b.URL, true, 1) // 1 byte cap — any decoded output exceeds this
	srv := httptest.NewServer(g.StreamingHandler())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(srv, "codec=opus"), &websocket.DialOptions{
		Subprotocols: []string{opusSubprotocol},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	conn.Write(ctx, websocket.MessageBinary, ogg)                 //nolint:errcheck
	done, _ := json.Marshal(map[string]string{"action": "done"})
	conn.Write(ctx, websocket.MessageText, done)                   //nolint:errcheck
	assertCloseCode(t, conn, websocket.StatusCode(wsCloseTooLarge))
}

// --- garbage bytes ---

func TestOpus_GarbageBytesUnknownContainer(t *testing.T) {
	// Bytes that aren't OggS or EBML magic → wsCloseUnknown (contract violation).
	b := echoTranscript(t, "nope")
	g := makeGateway(b.URL, false, 10<<20)
	srv := httptest.NewServer(g.StreamingHandler())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(srv, "codec=opus"), &websocket.DialOptions{
		Subprotocols: []string{opusSubprotocol},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	garbage := []byte("not an ogg or webm stream, just random bytes here")
	conn.Write(ctx, websocket.MessageBinary, garbage)             //nolint:errcheck
	done, _ := json.Marshal(map[string]string{"action": "done"})
	conn.Write(ctx, websocket.MessageText, done)                   //nolint:errcheck
	assertCloseCode(t, conn, websocket.StatusCode(wsCloseUnknown))
}

// --- subprotocol negotiation ---

func TestOpus_SubprotocolAcceptedReportsCodecTag(t *testing.T) {
	ogg := genOggOpus(t)
	var gotCodec, gotNegotiation atomic.Value
	b := echoTranscript(t, "negotiated")
	g := makeGateway(b.URL, false, 10<<20)
	g.OnStreamingCodec = func(_ context.Context, codec, negotiation string) {
		gotCodec.Store(codec)
		gotNegotiation.Store(negotiation)
	}
	srv := httptest.NewServer(g.StreamingHandler())
	t.Cleanup(srv.Close)

	text, err := streamOpusBinary(t, srv, ogg, true)
	if err != nil {
		t.Fatalf("streamOpusBinary: %v", err)
	}
	if text != "negotiated" {
		t.Errorf("text: want 'negotiated', got %q", text)
	}
	if c, _ := gotCodec.Load().(string); c != "opus" {
		t.Errorf("codec tag: want 'opus', got %q", c)
	}
	if n, _ := gotNegotiation.Load().(string); n != "accepted" {
		t.Errorf("negotiation: want 'accepted', got %q", n)
	}
}

func TestOpus_SubprotocolDeclinedFallsBackToPCM(t *testing.T) {
	// When ffmpeg is absent and NeedsWAV=true, the server cannot offer the Opus
	// subprotocol. A client that offered it falls back to PCM automatically.
	realPath := lookupFfmpeg()
	testSetFfmpegPath("")
	t.Cleanup(func() { testSetFfmpegPath(realPath) })

	b := echoTranscript(t, "pcm fallback")
	g := makeGateway(b.URL, true, 10<<20)
	srv := httptest.NewServer(g.StreamingHandler())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, wsURL(srv, "codec=opus"), &websocket.DialOptions{
		Subprotocols: []string{opusSubprotocol},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	if resp != nil && resp.Header.Get("Sec-WebSocket-Protocol") == opusSubprotocol {
		t.Error("server echoed subprotocol even though ffmpeg is unavailable")
	}

	conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200)) //nolint:errcheck
	done, _ := json.Marshal(map[string]string{"action": "done"})
	conn.Write(ctx, websocket.MessageText, done) //nolint:errcheck

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var result struct{ Text string }
	json.Unmarshal(data, &result) //nolint:errcheck
	if result.Text != "pcm fallback" {
		t.Errorf("text: want 'pcm fallback', got %q", result.Text)
	}
}

func TestOpus_NonNegotiatingClientNoFfmpegClosesCleanly(t *testing.T) {
	// A non-negotiating client (no subprotocol offered, codec=opus) on a
	// NeedsWAV backend with no ffmpeg must get wsCloseFailed, not a hang.
	realPath := lookupFfmpeg()
	testSetFfmpegPath("")
	t.Cleanup(func() { testSetFfmpegPath(realPath) })

	b := echoTranscript(t, "unreachable")
	g := makeGateway(b.URL, true, 10<<20)
	srv := httptest.NewServer(g.StreamingHandler())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(srv, "codec=opus"), nil /* no subprotocol */)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	assertCloseCode(t, conn, websocket.StatusCode(wsCloseFailed))
}

func TestOpus_PCMCodecTagReported(t *testing.T) {
	var gotCodec atomic.Value
	b := echoTranscript(t, "ok")
	g := makeGateway(b.URL, false, 10<<20)
	g.OnStreamingCodec = func(_ context.Context, codec, _ string) {
		gotCodec.Store(codec)
	}
	srv := httptest.NewServer(g.StreamingHandler())
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(srv, ""), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200)) //nolint:errcheck
	done, _ := json.Marshal(map[string]string{"action": "done"})
	conn.Write(ctx, websocket.MessageText, done) //nolint:errcheck
	conn.Read(ctx)                               //nolint:errcheck

	if c, _ := gotCodec.Load().(string); c != "pcm" {
		t.Errorf("codec tag: want 'pcm', got %q", c)
	}
}

// --- granulepos duration (pure-Go, no ffmpeg) ---

func TestOpus_OggDurationMs(t *testing.T) {
	tests := []struct {
		name    string
		granule int64
		preSkip uint16
		wantMs  int64
	}{
		{"zero granule", 0, 0, 0},
		{"exact 1s at 48 kHz", 48000, 0, 1000},
		{"1s with pre-skip", 48312, 312, 1000},
		{"negative granule (undefined)", -1, 0, 0},
		{"10 ms clip", 480, 0, 10},
		{"large clip 10s", 480000, 0, 10000},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			data := buildMinimalOggOpus(tc.granule, tc.preSkip)
			got := oggDurationMs(data)
			if got != tc.wantMs {
				t.Errorf("oggDurationMs: want %d ms, got %d ms", tc.wantMs, got)
			}
		})
	}
}

func TestOpus_OggDurationMsEmptySlice(t *testing.T) {
	if got := oggDurationMs(nil); got != 0 {
		t.Errorf("nil input: want 0, got %d", got)
	}
	if got := oggDurationMs([]byte{}); got != 0 {
		t.Errorf("empty input: want 0, got %d", got)
	}
}

// buildMinimalOggOpus creates a two-page Ogg file: BOS with OpusHead
// (containing preSkip), then one data page with the given granulepos.
// CRC is left zero — the helpers under test don't validate it.
func buildMinimalOggOpus(granule int64, preSkip uint16) []byte {
	var buf bytes.Buffer

	// OpusHead: magic(8) + version(1) + channels(1) + pre-skip(2) + sample-rate(4) + gain(2) + mapping(1) = 19 bytes
	var head [19]byte
	copy(head[:8], "OpusHead")
	head[8] = 1 // version
	head[9] = 1 // mono
	binary.LittleEndian.PutUint16(head[10:], preSkip)
	binary.LittleEndian.PutUint32(head[12:], 48000)

	writeOggPage(&buf, 0x02, 0, 0, 0, head[:])
	writeOggPage(&buf, 0x00, granule, 1, 0, []byte("opus-frame-placeholder"))
	return buf.Bytes()
}

// writeOggPage appends a minimal Ogg page to buf. CRC bytes are zero.
func writeOggPage(buf *bytes.Buffer, headerType byte, granulepos int64, pageSeq, serial uint32, payload []byte) {
	var hdr [27]byte
	copy(hdr[:], "OggS")
	hdr[5] = headerType
	binary.LittleEndian.PutUint64(hdr[6:], uint64(granulepos))
	binary.LittleEndian.PutUint32(hdr[14:], serial)
	binary.LittleEndian.PutUint32(hdr[18:], pageSeq)
	hdr[26] = 1 // one segment
	buf.Write(hdr[:])
	buf.WriteByte(byte(len(payload)))
	buf.Write(payload)
}

// --- WAV-header / ffmpeg-arg agreement ---

// TestOpus_WAVFormatAgreement asserts that the constants in WriteWAVHeader
// (16 kHz, mono, s16le) match what the ffmpeg decode command requests.
// If either side drifts, the backend receives mis-framed PCM.
func TestOpus_WAVFormatAgreement(t *testing.T) {
	const wantSampleRate = uint32(16000)
	const wantChannels = uint16(1)
	const wantBitsPerSample = uint16(16)

	var wavBuf bytes.Buffer
	if err := WriteWAVHeader(&wavBuf, 32000); err != nil {
		t.Fatalf("WriteWAVHeader: %v", err)
	}
	b := wavBuf.Bytes()
	if len(b) < 44 {
		t.Fatalf("WAV header too short: %d bytes", len(b))
	}
	if ch := binary.LittleEndian.Uint16(b[22:24]); ch != wantChannels {
		t.Errorf("channels: WAV=%d, ffmpeg -ac expects %d", ch, wantChannels)
	}
	if sr := binary.LittleEndian.Uint32(b[24:28]); sr != wantSampleRate {
		t.Errorf("sample rate: WAV=%d, ffmpeg -ar expects %d", sr, wantSampleRate)
	}
	if bps := binary.LittleEndian.Uint16(b[34:36]); bps != wantBitsPerSample {
		t.Errorf("bits per sample: WAV=%d, ffmpeg -f s16le expects %d", bps, wantBitsPerSample)
	}
}

// --- benchmark ---

func BenchmarkOpus_ConcurrentFFmpegDecode(b *testing.B) {
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		b.Skip("ffmpeg not in PATH")
	}
	testSetFfmpegPath(p)

	cmd := exec.Command(p, "-y",
		"-f", "lavfi", "-i", "anullsrc=r=48000:cl=mono",
		"-t", "2", "-c:a", "libopus", "-f", "ogg", "pipe:1",
	)
	var ogg bytes.Buffer
	cmd.Stdout = &ogg
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		b.Skipf("ffmpeg libopus encode failed: %v", err)
	}
	oggBytes := ogg.Bytes()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseMultipartForm(50 << 20) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"bench"}`)
	}))
	b.Cleanup(backend.Close)

	g := makeGateway(backend.URL, true, 50<<20)
	srv := httptest.NewServer(g.StreamingHandler())
	b.Cleanup(srv.Close)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			conn, _, err := websocket.Dial(ctx, wsURL(srv, "codec=opus"), &websocket.DialOptions{
				Subprotocols: []string{opusSubprotocol},
			})
			if err != nil {
				cancel()
				b.Errorf("dial: %v", err)
				continue
			}
			conn.Write(ctx, websocket.MessageBinary, oggBytes) //nolint:errcheck
			done, _ := json.Marshal(map[string]string{"action": "done"})
			conn.Write(ctx, websocket.MessageText, done) //nolint:errcheck
			conn.Read(ctx)                               //nolint:errcheck
			conn.CloseNow()
			cancel()
		}
	})
}
