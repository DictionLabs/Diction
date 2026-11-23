package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestBackCompat_ProductionClientUpstreamBytes(t *testing.T) {
	var (
		mu       sync.Mutex
		gotBody  []byte
		gotCT    string
		gotCount int
	)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 0, 1<<20)
		tmp := make([]byte, 32<<10)
		for {
			n, err := r.Body.Read(tmp)
			buf = append(buf, tmp[:n]...)
			if err != nil {
				break
			}
		}
		mu.Lock()
		gotBody = buf
		gotCT = r.Header.Get("Content-Type")
		gotCount++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"BACKCOMPAT_OK"}`)
	}))
	defer backend.Close()

	g := &Gateway{
		backends: []Backend{
			{Name: "small", URL: backend.URL, Aliases: []string{"small"}, NeedsWAV: true},
		},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}
	g.health.set("small", true)

	srv := httptest.NewServer(trackHandler(g.StreamingHandler()))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pcm := make([]byte, 32000)
	for i := range pcm {
		pcm[i] = byte((i * 37) % 256)
	}

	conn, _, err := websocket.Dial(ctx, wsURL(srv, "language=en"), &websocket.DialOptions{})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	if sub := conn.Subprotocol(); sub != "" {
		t.Errorf("BACK-COMPAT VIOLATION: server negotiated subprotocol %q with a client that offered none", sub)
	}

	for i := 0; i < len(pcm); i += 682 {
		end := i + 682
		if end > len(pcm) {
			end = len(pcm)
		}
		if err := conn.Write(ctx, websocket.MessageBinary, pcm[i:end]); err != nil {
			t.Fatalf("write frame at %d: %v", i, err)
		}
	}
	done, _ := json.Marshal(map[string]string{"action": "done"})
	if err := conn.Write(ctx, websocket.MessageText, done); err != nil {
		t.Fatalf("write done: %v", err)
	}

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	var result struct{ Text string }
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal %q: %v", data, err)
	}
	if result.Text != "BACKCOMPAT_OK" {
		t.Errorf("transcript: want BACKCOMPAT_OK, got %q", result.Text)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotCount != 1 {
		t.Fatalf("backend hit %d times, want exactly 1", gotCount)
	}

	audio := extractWAVPart(gotBody)
	if len(audio) == 0 {
		t.Fatalf("no WAV payload found in upstream body (len=%d, ct=%q)", len(gotBody), gotCT)
	}

	const preOpusWAVSHA256 = "4ec81abba5758d66f3612761d8aa0fdd56b77c55f0a0d87766ee480a83c66153"
	const preOpusWAVLen = 32044

	gotSHA := fmt.Sprintf("%x", sha256.Sum256(audio))
	if gotSHA != preOpusWAVSHA256 {
		t.Errorf("BACK-COMPAT VIOLATION: upstream WAV bytes changed for a pre-Opus client.\n"+
			"  pre-Opus sha256 = %s\n  current  sha256 = %s", preOpusWAVSHA256, gotSHA)
	}
	if len(audio) != preOpusWAVLen {
		t.Errorf("BACK-COMPAT VIOLATION: upstream WAV length changed: want %d, got %d",
			preOpusWAVLen, len(audio))
	}
}

func extractWAVPart(body []byte) []byte {
	start := -1
	for i := 0; i+4 <= len(body); i++ {
		if body[i] == 'R' && body[i+1] == 'I' && body[i+2] == 'F' && body[i+3] == 'F' {
			start = i
			break
		}
	}
	if start < 0 {
		return nil
	}
	if start+8 > len(body) {
		return nil
	}
	size := int(body[start+4]) | int(body[start+5])<<8 | int(body[start+6])<<16 | int(body[start+7])<<24
	end := start + 8 + size
	if end > len(body) {
		end = len(body)
	}
	return body[start:end]
}
