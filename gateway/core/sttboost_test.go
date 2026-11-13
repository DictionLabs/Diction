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

// A saved word that was said wins, boosted STT or not (2026-11-05, reversing the 2026-10-18
// "boosted STT wrote another real name, drop the entry" rule): the entry reaches the LLM with a
// pointer at what was heard, and the LLM decides from the sentence.
func TestMatchVocabulary_SavedWordWinsOverRealName(t *testing.T) {
	forms := [][]string{{"Kristýna"}, {"Machala"}, {"Ondřej"}, {"Nemotron"}, {"Joaquín"}, {"Vantagio", "Vantage"}}
	for _, tc := range []struct {
		transcript string
		want       string // shortlist joined by ",", same boosted and unboosted
	}{
		{"Kristina from accounting asked about the invoice.", "Kristýna<-Kristina"},
		{"Michaela will send the photos tonight.", "Machala<-Michaela"},
		{"Ondra přijde zítra na oběd.", "Ondřej<-Ondra"},
		{"Andrej přijede v pondělí ráno.", "Ondřej<-Andrej"},
		{"We tested Namotron on the GPU.", "Nemotron<-Namotron"},
		{"Joaquin is coming tomorrow.", "Joaquín<-Joaquin"},
		{"Vantage shipped the update.", "Vantagio<-Vantage"},
		{"Hi, I am Andrej Machala.", "Ondřej<-Andrej,Machala"},
	} {
		for _, boosted := range []bool{true, false} {
			if got := strings.Join(shortlistWords(tc.transcript, forms, boosted), ","); got != tc.want {
				t.Errorf("boosted=%v %q: got %q want %q", boosted, tc.transcript, got, tc.want)
			}
		}
	}
}

// 2026-11-05 production report: My Words Ondřej, Kristýna, Cara, rig; Cohere (boosted) wrote
// Andre, Christina, Kara, Rick and the gateway logged "shortlist 0 of 15" on every dictation.
// Each must reach the shortlist with a pointer at the heard word.
func TestMatchVocabulary_OwnerSavedWordsReachShortlist(t *testing.T) {
	forms := [][]string{{"Ondřej"}, {"Kristýna"}, {"Cara"}, {"rig"}, {"Machala"}, {"Fennec"}}
	for _, tc := range []struct{ transcript, want string }{
		{"I talked to Christina yesterday", "Kristýna<-Christina"},
		{"I talked to Kristina yesterday", "Kristýna<-Kristina"},
		{"Andre said hi", "Ondřej<-Andre"},
		{"Andrej said hi", "Ondřej<-Andrej"},
		{"Deploy it on Kara tonight", "Cara<-Kara"},
		{"The Rick is running hot", "rig<-Rick"},
		// The saved spelling itself: listed, nothing to point at.
		{"Cara called about the rig", "Cara,rig"},
	} {
		for _, boosted := range []bool{true, false} {
			if got := strings.Join(shortlistWords(tc.transcript, forms, boosted), ","); got != tc.want {
				t.Errorf("boosted=%v %q: got %q want %q", boosted, tc.transcript, got, tc.want)
			}
		}
	}
}

// Short saved words (two-consonant sound keys) match only a lone, non-ordinary word with the
// same first letter and at most one letter apart; ordinary words and other names stay out.
func TestMatchVocabulary_ShortSavedWordRejectsCoincidences(t *testing.T) {
	forms := [][]string{{"Cara"}, {"rig"}}
	for _, transcript := range []string{
		"I parked the car in the garage.",
		"We need more cores on the rack.",
		"Get rid of the old ring.",
		"The rug is red.",
		"Rio is lovely in spring.",
		"Cora and Kurt came over.",
	} {
		for _, boosted := range []bool{true, false} {
			if got := shortlistWords(transcript, forms, boosted); len(got) != 0 {
				t.Errorf("boosted=%v %q: got %v, want none", boosted, transcript, got)
			}
		}
	}
}

// The real request shape: every saved word said reaches <custom_words> with its pointer.
func TestBuildCleanupUserMessage_BoostedOwnerWords(t *testing.T) {
	m := CleanupMessage{
		Transcript: "I talked to Christina and Andre about Kara and the Rick",
		VocabForms: [][]string{{"Ondřej"}, {"Kristýna"}, {"Cara"}, {"rig"}},
		STTBoosted: true,
		Closing:    CleanupClosing,
	}
	msg, _ := BuildCleanupUserMessage(m)
	for _, line := range []string{
		`Kristýna (sounds like "Christina" here)`,
		`Ondřej (sounds like "Andre" here)`,
		`Cara (sounds like "Kara" here)`,
		`rig (sounds like "Rick" here)`,
	} {
		if !strings.Contains(msg, line) {
			t.Errorf("missing %s in:\n%s", line, msg)
		}
	}
	m.Phone = []PhoneTranscript{{Model: "parakeet", Text: m.Transcript}}
	if msg, _ := BuildCleanupUserMessage(m); !strings.Contains(msg, "Kristýna") {
		t.Errorf("merge must keep the entry:\n%s", msg)
	}
}
