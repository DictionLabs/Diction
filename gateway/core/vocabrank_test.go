package core

import (
	"reflect"
	"strings"
	"testing"
)

// entry builds one vocabulary entry: canonical spelling first, variants after.
func entry(forms ...string) []string { return forms }

func words(forms [][]string, idx []int) []string {
	out := make([]string, 0, len(idx))
	for _, i := range idx {
		out = append(out, forms[i][0])
	}
	return out
}

func TestRankVocabulary(t *testing.T) {
	cases := []struct {
		name       string
		transcript string
		forms      [][]string
		want       []string
	}{
		{
			name:       "misheard surname is admitted",
			transcript: "I spoke with Ondrej Mahala yesterday about the invoice",
			forms:      [][]string{entry("Machala")},
			want:       []string{"Machala"},
		},
		{
			name:       "exact spelling already present is admitted so it is protected",
			transcript: "I spoke with Ondrej Machala yesterday",
			forms:      [][]string{entry("Machala")},
			want:       []string{"Machala"},
		},
		{
			name:       "czech name behind an english mishearing",
			transcript: "we drove to set in on Friday",
			forms:      [][]string{entry("Vsetín")},
			want:       []string{"Vsetín"},
		},
		{
			name:       "christina reaches kristyna through the soft-c rule",
			transcript: "I spoke with Christina today about the project timeline",
			forms:      [][]string{entry("Kristýna")},
			want:       []string{"Kristýna"},
		},
		{
			name:       "variant carries an entry the canonical spelling cannot reach",
			transcript: "I was using the ditching keyboard today and it worked great",
			forms:      [][]string{entry("Diction", "ditching", "diction app")},
			want:       []string{"Diction"},
		},
		{
			name:       "multi-token mangling matches a single word",
			transcript: "we deployed on cube netties using helm",
			forms:      [][]string{entry("Kubernetes", "cube netties")},
			want:       []string{"Kubernetes"},
		},
		{
			name:       "three-token mangling matches a compound term",
			transcript: "the recording life cycle was throwing an error in the logs",
			forms:      [][]string{entry("RecordingLifecycle", "recording life cycle")},
			want:       []string{"RecordingLifecycle"},
		},
		{
			name:       "word absent from the transcript is excluded",
			transcript: "the weather was nice today and I went for a walk",
			forms:      [][]string{entry("Kubernetes"), entry("RecordingLifecycle")},
			want:       nil,
		},
		{
			name:       "short ordinary words never pull in short names",
			transcript: "a walk in the park is nice",
			forms:      [][]string{entry("Ava"), entry("Iris")},
			want:       nil,
		},
		{
			name:       "an ordinary word is not offered up for substitution",
			transcript: "a walk in the park is nice at this time of year",
			forms:      [][]string{entry("Parker")},
			want:       nil,
		},
		{
			name:       "a custom word that really is an ordinary word still matches exactly",
			transcript: "meet me in the park at noon",
			forms:      [][]string{entry("Park")},
			want:       []string{"Park"},
		},
		{
			name:       "protection is per token, so a split mishearing still matches",
			transcript: "we drove to set in on Friday",
			forms:      [][]string{entry("Vsetín")},
			want:       []string{"Vsetín"},
		},
		{
			// 2026-09-24 user report: names said in English, every one misheard but the last.
			// Only Fennec reached the prompt, because its exact hit tightened the bar for the
			// whole utterance.
			name:       "several words misheard in one utterance are all admitted",
			transcript: "Hey, my name is Andre McCullough and I have two servers. One is called Quill and the second one Fennec.",
			forms:      [][]string{entry("ondrej"), entry("machala"), entry("kuiil"), entry("fennec")},
			want:       []string{"ondrej", "machala", "kuiil", "fennec"},
		},
		{
			name:       "an exact hit elsewhere does not suppress a near match",
			transcript: "Andre runs the Fennec server",
			forms:      [][]string{entry("Ondrej"), entry("Fennec")},
			want:       []string{"Ondrej", "Fennec"},
		},
		{
			// The slack still works where it was meant to: between rivals for the same word.
			name:       "a near rival for the same word is still dropped beside an exact hit",
			transcript: "the second server is Fennec",
			forms:      [][]string{entry("Fenwick"), entry("Fennec")},
			want:       []string{"Fennec"},
		},
		{
			name:       "english mishearing of a czech surname",
			transcript: "my name is Andre McCullough",
			forms:      [][]string{entry("Machala")},
			want:       []string{"Machala"},
		},
		{
			name:       "a short-fold name is reachable from its mishearing",
			transcript: "the server is called Quill",
			forms:      [][]string{entry("Kuiil")},
			want:       []string{"Kuiil"},
		},
		{
			// 2026-09-25 user report: "we will put home TLD" came back as "Kuiil put home TLD".
			// Kuiil folds to three runes, so one edit ("we will" → "vvl") sat inside the ceiling
			// and the prompt pointed the LLM straight at an ordinary phrase.
			name:       "a short-fold name is not matched one edit away",
			transcript: "I believe we do, right? Because yes, we will put home TLD.",
			forms:      [][]string{entry("Kuiil")},
			want:       nil,
		},
		{
			name:       "empty transcript admits nothing",
			transcript: "",
			forms:      [][]string{entry("Machala")},
			want:       nil,
		},
		{
			name:       "empty list admits nothing",
			transcript: "I spoke with Ondrej Mahala",
			forms:      nil,
			want:       nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := words(tc.forms, RankVocabulary(tc.transcript, tc.forms, DefaultVocabularyShortlist))
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("RankVocabulary() = %v, want %v", got, tc.want)
			}
		})
	}
}

// The measured failure of the old behaviour: a list of unrelated words no longer reaches the
// prompt, and the one word that matters does — and sits last, where the LLM weights it most.
func TestRankVocabulary_DistractorsAreDroppedAndBestSitsLast(t *testing.T) {
	forms := [][]string{}
	for _, w := range []string{
		"Kubernetes", "Helmfile", "Terraform", "Grafana", "Prometheus", "Postgres",
		"Redis", "Elasticsearch", "Kafka", "RabbitMQ", "Jenkins", "Ansible",
		"Datadog", "Sentry", "Cloudflare", "Fastly", "Snowflake", "Databricks",
		"Airflow", "Kubeflow", "Istio", "Envoy", "Consul", "Vault",
		"Nomad", "Packer", "Vagrant", "Podman", "Containerd", "Buildkite",
	} {
		forms = append(forms, entry(w))
	}
	forms = append(forms, entry("Machala"))

	idx := RankVocabulary("I spoke with Mahala yesterday about the invoice", forms, DefaultVocabularyShortlist)
	got := words(forms, idx)
	if len(got) == 0 {
		t.Fatal("expected the near match to be admitted")
	}
	if len(got) > DefaultVocabularyShortlist {
		t.Errorf("admitted %d entries, cap is %d", len(got), DefaultVocabularyShortlist)
	}
	if got[len(got)-1] != "Machala" {
		t.Errorf("best match must sit last, got %v", got)
	}
	for _, w := range got {
		if w == "Terraform" || w == "Grafana" || w == "Jenkins" {
			t.Errorf("distractor %q reached the prompt: %v", w, got)
		}
	}
}

func TestRankVocabulary_RespectsLimit(t *testing.T) {
	// Twelve spellings of the same sound: all are credible, so the cap is what bounds them.
	forms := [][]string{}
	for _, w := range []string{
		"Machala", "Machalla", "Machalah", "Makhala", "Mahchala", "Machalo",
		"Machaley", "Machalie", "Machalu", "Machalir", "Machalen", "Machalos",
	} {
		forms = append(forms, entry(w))
	}
	idx := RankVocabulary("I spoke with Mahala yesterday", forms, 3)
	if len(idx) > 3 {
		t.Fatalf("limit not applied: got %d", len(idx))
	}
}

func TestRankVocabulary_ZeroLimitAdmitsNothing(t *testing.T) {
	if idx := RankVocabulary("Mahala", [][]string{entry("Machala")}, 0); idx != nil {
		t.Errorf("expected nil for a zero limit, got %v", idx)
	}
}

// A list far past the guard must still answer, and must still find the match inside it.
func TestRankVocabulary_LargeListIsBounded(t *testing.T) {
	forms := make([][]string, 0, vocabMaxEntries+50)
	forms = append(forms, entry("Machala"))
	for i := 0; i < vocabMaxEntries+49; i++ {
		forms = append(forms, entry("Placeholder"+strings.Repeat("x", i%7+3)))
	}
	got := words(forms, RankVocabulary("I spoke with Mahala today", forms, DefaultVocabularyShortlist))
	if len(got) == 0 || got[len(got)-1] != "Machala" {
		t.Errorf("expected Machala last, got %v", got)
	}
}

// The fold is a table of judgement calls; these are the ones the product depends on.
func TestFoldPhonetic_PinsTheCasesTheProductRelieson(t *testing.T) {
	same := [][2]string{
		{"Vsetín", "Vsetin"},         // diacritic only
		{"Kubernetes", "Cubernetes"}, // hard c → k
		{"photo", "foto"},            // ph → f
		{"Mueller", "Muller"},        // doubled letters collapse
		{"McCullough", "Mccullo"},    // silent non-initial gh
		{"Kuiil", "Quill"},           // ku before a vowel sounds like qu
		{"night", "nit"},             // silent gh before t
	}
	for _, pair := range same {
		if a, b := foldPhonetic(pair[0]), foldPhonetic(pair[1]); a != b {
			t.Errorf("foldPhonetic(%q)=%q != foldPhonetic(%q)=%q", pair[0], a, pair[1], b)
		}
	}

	// The two names the product turns on are *near*, not identical: no single "ch" rule can
	// serve Czech and English at once (Christina opens with a /k/, Machala with an /x/), so
	// edit distance carries the difference instead. These bounds are what keep both
	// admissible — a fold change that widens either one breaks a real case.
	near := []struct {
		a, b string
		max  float64
	}{
		{"Machala", "Mahala", 0.30},
		{"Christina", "Kristýna", 0.20},
	}
	for _, pair := range near {
		if d := normalizedLevenshtein(foldPhonetic(pair.a), foldPhonetic(pair.b)); d > pair.max {
			t.Errorf("fold distance %q↔%q = %.3f, want ≤ %.2f", pair.a, pair.b, d, pair.max)
		}
	}

	differ := [][2]string{
		{"Machala", "Kubernetes"},
		{"Ava", "Iris"},
		{"park", "Parker"},
	}
	for _, pair := range differ {
		if a, b := foldPhonetic(pair[0]), foldPhonetic(pair[1]); a == b {
			t.Errorf("foldPhonetic collapsed %q and %q both to %q", pair[0], pair[1], a)
		}
	}
}

// The shortlist knows which part of the transcript each word matched; handing that span to the
// LLM is what turns "Andre McCullough" into "Ondrej Machala" (without it the model kept the
// transcript 0/4 on gpt-oss-120b, with it 4/4 — 2026-09-24).
func TestMatchVocabulary_ReportsWhatWasHeard(t *testing.T) {
	forms := [][]string{entry("ondrej"), entry("machala"), entry("kuiil"), entry("fennec")}
	got := MatchVocabulary(
		"Hey, my name is Andre McCullough and I have two servers. One is called Quill and the second one Fennec.",
		forms, DefaultVocabularyShortlist)
	heard := map[string]string{}
	for _, m := range got {
		heard[forms[m.Index][0]] = m.Heard
	}
	want := map[string]string{"ondrej": "Andre", "machala": "McCullough", "kuiil": "Quill", "fennec": ""}
	if !reflect.DeepEqual(heard, want) {
		t.Errorf("heard = %v, want %v", heard, want)
	}
}

// A span of ordinary words is still shortlisted but gets no pointer. With the pointer,
// gpt-oss-120b turned "I left the keys on the door." into "I left the keys Ondrej." (2026-09-26);
// without it the LLM keeps the sentence and still restores misheard names.
func TestMatchVocabulary_NoPointerAtOrdinaryWords(t *testing.T) {
	forms := [][]string{entry("ondrej"), entry("machala")}
	got := MatchVocabulary("I left the keys on the door.", forms, DefaultVocabularyShortlist)
	if len(got) != 1 || forms[got[0].Index][0] != "ondrej" {
		t.Fatalf("shortlist = %v, want only ondrej", got)
	}
	if got[0].Heard != "" {
		t.Errorf("Heard = %q, want no pointer at an ordinary-word span", got[0].Heard)
	}
}

// RankVocabulary stays the index-only view of the same answer, for existing callers.
func TestRankVocabulary_MatchesMatchVocabularyOrder(t *testing.T) {
	forms := [][]string{entry("ondrej"), entry("fennec")}
	tr := "Andre runs the Fennec server"
	var fromMatch []int
	for _, m := range MatchVocabulary(tr, forms, DefaultVocabularyShortlist) {
		fromMatch = append(fromMatch, m.Index)
	}
	if got := RankVocabulary(tr, forms, DefaultVocabularyShortlist); !reflect.DeepEqual(got, fromMatch) {
		t.Errorf("RankVocabulary = %v, MatchVocabulary = %v", got, fromMatch)
	}
}

func TestCustomWordLine(t *testing.T) {
	cases := []struct {
		word     string
		variants []string
		heard    string
		want     string
	}{
		{"Fennec", nil, "", "Fennec"},
		{"Machala", nil, "McCullough", `Machala (sounds like "McCullough" here)`},
		{"Diction", []string{"ditching", "diction app"}, "", "Diction (also heard as: ditching, diction app)"},
		{"Diction", []string{"ditching"}, "ditching", `Diction (also heard as: ditching; sounds like "ditching" here)`},
	}
	for _, tc := range cases {
		if got := CustomWordLine(tc.word, tc.variants, tc.heard); got != tc.want {
			t.Errorf("CustomWordLine(%q, %v, %q) = %q, want %q", tc.word, tc.variants, tc.heard, got, tc.want)
		}
	}
}
