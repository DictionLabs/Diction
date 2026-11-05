package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Writing Tools × STT boost: the cleanup learns whether the backend that actually served the
// transcript was sent the custom-word list, and a boosted STT's lone real name drops the
// matching My Word from the shortlist. Record:
// .claude/bow/writing-tools-mywords-cooperation-2026-10.md.

// boostRecorder is a postProcess that records whether its ctx says the STT was boosted.
type boostRecorder struct {
	mu   sync.Mutex
	seen []bool
}

func (b *boostRecorder) postProcess(ctx context.Context, text, _, _ string) (string, string, error) {
	b.mu.Lock()
	b.seen = append(b.seen, STTBoosted(ctx))
	b.mu.Unlock()
	return text, "transcribe", nil
}

func (b *boostRecorder) snapshot() []bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]bool(nil), b.seen...)
}

func boostTestGateway(t *testing.T, primaryStatus int, primaryFlagged bool) *Gateway {
	t.Helper()
	primary := (&fieldRecorder{}).server(t, primaryStatus)
	fallback := (&fieldRecorder{}).server(t, http.StatusOK)
	g := &Gateway{
		backends: []Backend{
			{Name: "primary", URL: primary.URL, Aliases: []string{"primary"}, AcceptsCustomWords: primaryFlagged},
			{Name: "fallback", URL: fallback.URL, Aliases: []string{"fallback"}},
		},
		health:        newHealthState(),
		defaultModel:  "primary",
		fallbackModel: "fallback",
		maxBodySize:   10 * 1024 * 1024,
	}
	g.health.set("primary", true)
	g.health.set("fallback", true)
	return g
}

var boostCases = []struct {
	name           string
	primaryStatus  int
	primaryFlagged bool
	want           bool
}{
	{"flagged primary serves", http.StatusOK, true, true},
	{"unflagged primary serves", http.StatusOK, false, false},
	{"retry onto unflagged fallback", http.StatusInternalServerError, true, false},
}

func TestTranscriptionHandler_PostProcessSeesSTTBoost(t *testing.T) {
	for _, tc := range boostCases {
		t.Run(tc.name, func(t *testing.T) {
			g := boostTestGateway(t, tc.primaryStatus, tc.primaryFlagged)
			rec := &boostRecorder{}
			body, ct := buildMultipart(t, map[string]string{"context": cwContext, "language": "en"}, "audio.m4a", "fake-audio")
			req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions?enhance=true", bytes.NewReader(body))
			req.Header.Set("Content-Type", ct)
			rr := httptest.NewRecorder()
			g.TranscriptionHandlerWithPostProcess(rec.postProcess)(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("status %d", rr.Code)
			}
			if got := rec.snapshot(); len(got) != 1 || got[0] != tc.want {
				t.Errorf("postProcess saw boosted=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestStreamHandler_PostProcessSeesSTTBoost(t *testing.T) {
	for _, tc := range boostCases {
		t.Run(tc.name, func(t *testing.T) {
			g := boostTestGateway(t, tc.primaryStatus, tc.primaryFlagged)
			rec := &boostRecorder{}
			srv := httptest.NewServer(g.StreamingHandlerWithPostProcess(rec.postProcess))
			t.Cleanup(srv.Close)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			conn, _, err := websocket.Dial(ctx, wsURL(srv, "language=en&enhance=true"), nil)
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer conn.CloseNow()
			if err := conn.Write(ctx, websocket.MessageText, []byte(cwContext)); err != nil {
				t.Fatal(err)
			}
			if err := conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200)); err != nil {
				t.Fatal(err)
			}
			done, _ := json.Marshal(map[string]string{"action": "done"})
			if err := conn.Write(ctx, websocket.MessageText, done); err != nil {
				t.Fatal(err)
			}
			if _, _, err := conn.Read(ctx); err != nil {
				t.Fatalf("read result: %v", err)
			}
			if got := rec.snapshot(); len(got) != 1 || got[0] != tc.want {
				t.Errorf("postProcess saw boosted=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsDictionaryWord(t *testing.T) {
	for _, w := range []string{"Kristina", "michaela", "Andrej", "Ondra", "Grafen", "graf", "the", "Наталья", "gráfico"} {
		if !isDictionaryWord(w) {
			t.Errorf("%q should be a dictionary word", w)
		}
	}
	for _, w := range []string{"Namotron", "Subabase", "Machila", "Zachidl", "Machala", "", "zzzzzz"} {
		if isDictionaryWord(w) {
			t.Errorf("%q should not be a dictionary word", w)
		}
	}
}

// shortlistWords renders MatchVocabularyWith as "Word" / "Word<-heard" strings.
func shortlistWords(transcript string, forms [][]string, boosted bool) []string {
	var out []string
	for _, m := range MatchVocabularyWith(transcript, forms, DefaultVocabularyShortlist, VocabularyOptions{STTBoosted: boosted}) {
		s := forms[m.Index][0]
		if m.Heard != "" {
			s += "<-" + m.Heard
		}
		out = append(out, s)
	}
	return out
}

func TestMatchVocabulary_BoostedSTTLoneRealNameDropsEntry(t *testing.T) {
	forms := [][]string{{"Kristýna"}, {"Machala"}, {"Ondřej"}, {"Nemotron"}, {"Joaquín"}, {"Vantagio", "Vantage"}}
	for _, tc := range []struct {
		transcript         string
		boosted, unboosted string // shortlist joined by ","
	}{
		// Another person's name, one letter off a saved one: gone when the STT had the list.
		{"Kristina from accounting asked about the invoice.", "", "Kristýna"},
		// The saved word is not a real name (Machala): the entry stays, the swap stays open.
		{"Michaela will send the photos tonight.", "Machala", "Machala"},
		{"Ondra přijde zítra na oběd.", "", "Ondřej"},
		{"Andrej přijede v pondělí ráno.", "", "Ondřej"},
		// A mangling of a saved word is not a real word: kept, pointer-less as before.
		{"We tested Namotron on the GPU.", "Nemotron", "Nemotron"},
		// Diacritics only: that is the saved word.
		{"Joaquin is coming tomorrow.", "Joaquín", "Joaquín"},
		// A taught variant keeps its pointer even when it is a real word.
		{"Vantage shipped the update.", "Vantagio<-Vantage", "Vantagio<-Vantage"},
		// Two names side by side are a full name, not a lone word.
		{"Hi, I am Andrej Machala.", "Ondřej<-Andrej,Machala", "Ondřej<-Andrej,Machala"},
	} {
		got := strings.Join(shortlistWords(tc.transcript, forms, true), ",")
		if got != tc.boosted {
			t.Errorf("boosted %q: got %q want %q", tc.transcript, got, tc.boosted)
		}
		got = strings.Join(shortlistWords(tc.transcript, forms, false), ",")
		if got != tc.unboosted {
			t.Errorf("unboosted %q: got %q want %q", tc.transcript, got, tc.unboosted)
		}
	}
}

func TestBuildCleanupUserMessage_STTBoostedIgnoredForMerge(t *testing.T) {
	m := CleanupMessage{
		Transcript: "Kristina from accounting asked about the invoice.",
		VocabForms: [][]string{{"Kristýna"}},
		STTBoosted: true,
		Closing:    CleanupClosing,
	}
	if msg, _ := BuildCleanupUserMessage(m); strings.Contains(msg, "<custom_words>") {
		t.Errorf("boosted single transcript should drop Kristýna:\n%s", msg)
	}
	m.Phone = []PhoneTranscript{{Model: "parakeet", Text: "Kristina from accounting asked about the invoice."}}
	if msg, _ := BuildCleanupUserMessage(m); !strings.Contains(msg, "Kristýna") {
		t.Errorf("merge must keep the entry (phones were not boosted):\n%s", msg)
	}
}
