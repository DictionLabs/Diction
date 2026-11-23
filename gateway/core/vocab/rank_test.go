package vocab

import (
	"reflect"
	"strings"
	"testing"
)

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
			transcript: "I spoke with Endre Mohala yesterday about the invoice",
			forms:      [][]string{entry("Mochala")},
			want:       []string{"Mochala"},
		},
		{
			name:       "exact spelling already present is admitted so it is protected",
			transcript: "I spoke with Endre Mochala yesterday",
			forms:      [][]string{entry("Mochala")},
			want:       []string{"Mochala"},
		},
		{
			name:       "czech name behind an english mishearing",
			transcript: "we drove to set in on Friday",
			forms:      [][]string{entry("Vsetín")},
			want:       []string{"Vsetín"},
		},
		{
			name:       "charlotte reaches karlota through the ch-to-k edit distance",
			transcript: "I spoke with Charlotte today about the project timeline",
			forms:      [][]string{entry("Karlota")},
			want:       []string{"Karlota"},
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
			name:       "several words misheard in one utterance are all admitted",
			transcript: "Hey, my name is Andre McCullough and I have two servers. One is called Quill and the second one Fennec.",
			forms:      [][]string{entry("endre"), entry("mochala"), entry("kuiil"), entry("fennec")},
			want:       []string{"endre", "mochala", "kuiil", "fennec"},
		},
		{
			name:       "an exact hit elsewhere does not suppress a near match",
			transcript: "Andre runs the Fennec server",
			forms:      [][]string{entry("Endre"), entry("Fennec")},
			want:       []string{"Endre", "Fennec"},
		},
		{
			name:       "a near rival for the same word is still dropped beside an exact hit",
			transcript: "the second server is Fennec",
			forms:      [][]string{entry("Fenwick"), entry("Fennec")},
			want:       []string{"Fennec"},
		},
		{
			name:       "english mishearing of a czech surname",
			transcript: "my name is Andre McCullough",
			forms:      [][]string{entry("Endre"), entry("Mochala")},
			want:       []string{"Endre", "Mochala"},
		},
		{
			name:       "a surname that shares only consonants is not enough on its own",
			transcript: "my name is Andre McCullough",
			forms:      [][]string{entry("Mochala")},
			want:       nil,
		},
		{
			name:       "consonant-skeleton coincidences are not shortlisted",
			transcript: "Hi Siobhan, can we move the call to Thursday afternoon so I can reach the keyboard is",
			forms:      [][]string{entry("tirzepatide"), entry("Joaquín"), entry("Kubernetes")},
			want:       nil,
		},
		{
			name:       "czech ordinary word against a consonant skeleton",
			transcript: "Notebook jsem připojil přes Tariel zcela.",
			forms:      [][]string{entry("Apiaries")},
			want:       nil,
		},
		{
			name:       "a short-fold name is reachable from its mishearing",
			transcript: "the server is called Quill",
			forms:      [][]string{entry("Kuiil")},
			want:       []string{"Kuiil"},
		},
		{
			name:       "a short-fold name is not matched one edit away",
			transcript: "I believe we do, right? Because yes, we will put home TLD.",
			forms:      [][]string{entry("Kuiil")},
			want:       nil,
		},
		{
			name:       "empty transcript admits nothing",
			transcript: "",
			forms:      [][]string{entry("Mochala")},
			want:       nil,
		},
		{
			name:       "empty list admits nothing",
			transcript: "I spoke with Endre Mohala",
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
	forms = append(forms, entry("Mochala"))

	idx := RankVocabulary("I spoke with Mohala yesterday about the invoice", forms, DefaultVocabularyShortlist)
	got := words(forms, idx)
	if len(got) == 0 {
		t.Fatal("expected the near match to be admitted")
	}
	if len(got) > DefaultVocabularyShortlist {
		t.Errorf("admitted %d entries, cap is %d", len(got), DefaultVocabularyShortlist)
	}
	if got[len(got)-1] != "Mochala" {
		t.Errorf("best match must sit last, got %v", got)
	}
	for _, w := range got {
		if w == "Terraform" || w == "Grafana" || w == "Jenkins" {
			t.Errorf("distractor %q reached the prompt: %v", w, got)
		}
	}
}

func TestRankVocabulary_RespectsLimit(t *testing.T) {
	forms := [][]string{}
	for _, w := range []string{
		"Mochala", "Mochalla", "Mochalah", "Mokhala", "Mohchala", "Mochalo",
		"Mochaley", "Mochalie", "Mochalu", "Mochalir", "Mochalen", "Mochalos",
	} {
		forms = append(forms, entry(w))
	}
	idx := RankVocabulary("I spoke with Mohala yesterday", forms, 3)
	if len(idx) > 3 {
		t.Fatalf("limit not applied: got %d", len(idx))
	}
}

func TestRankVocabulary_ZeroLimitAdmitsNothing(t *testing.T) {
	if idx := RankVocabulary("Mohala", [][]string{entry("Mochala")}, 0); idx != nil {
		t.Errorf("expected nil for a zero limit, got %v", idx)
	}
}

func TestRankVocabulary_LargeListIsBounded(t *testing.T) {
	forms := make([][]string, 0, vocabMaxEntries+50)
	forms = append(forms, entry("Mochala"))
	for i := 0; i < vocabMaxEntries+49; i++ {
		forms = append(forms, entry("Placeholder"+strings.Repeat("x", i%7+3)))
	}
	got := words(forms, RankVocabulary("I spoke with Mohala today", forms, DefaultVocabularyShortlist))
	if len(got) == 0 || got[len(got)-1] != "Mochala" {
		t.Errorf("expected Mochala last, got %v", got)
	}
}

func TestFoldPhonetic_PinsTheCasesTheProductRelieson(t *testing.T) {
	same := [][2]string{
		{"Vsetín", "Vsetin"},
		{"Kubernetes", "Cubernetes"},
		{"photo", "foto"},
		{"Mueller", "Muller"},
		{"McCullough", "Mccullo"},
		{"Kuiil", "Quill"},
		{"night", "nit"},
	}
	for _, pair := range same {
		if a, b := foldPhonetic(pair[0]), foldPhonetic(pair[1]); a != b {
			t.Errorf("foldPhonetic(%q)=%q != foldPhonetic(%q)=%q", pair[0], a, pair[1], b)
		}
	}

	near := []struct {
		a, b string
		max  float64
	}{
		{"Mochala", "Mohala", 0.30},
		{"Charlotte", "Karlota", 0.20},
	}
	for _, pair := range near {
		if d := normalizedLevenshtein(foldPhonetic(pair.a), foldPhonetic(pair.b)); d > pair.max {
			t.Errorf("fold distance %q↔%q = %.3f, want ≤ %.2f", pair.a, pair.b, d, pair.max)
		}
	}

	differ := [][2]string{
		{"Mochala", "Kubernetes"},
		{"Ava", "Iris"},
		{"park", "Parker"},
	}
	for _, pair := range differ {
		if a, b := foldPhonetic(pair[0]), foldPhonetic(pair[1]); a == b {
			t.Errorf("foldPhonetic collapsed %q and %q both to %q", pair[0], pair[1], a)
		}
	}
}

func TestMatchVocabulary_ReportsWhatWasHeard(t *testing.T) {
	forms := [][]string{entry("endre"), entry("mochala"), entry("kuiil"), entry("fennec")}
	got := MatchVocabulary(
		"Hey, my name is Andre McCullough and I have two servers. One is called Quill and the second one Fennec.",
		forms, DefaultVocabularyShortlist)
	heard := map[string]string{}
	for _, m := range got {
		heard[forms[m.Index][0]] = m.Heard
	}
	want := map[string]string{"endre": "Andre", "mochala": "McCullough", "kuiil": "Quill", "fennec": ""}
	if !reflect.DeepEqual(heard, want) {
		t.Errorf("heard = %v, want %v", heard, want)
	}
}

func TestMatchVocabulary_OrdinaryPhraseNotShortlisted(t *testing.T) {
	forms := [][]string{entry("endre"), entry("mochala")}
	if got := MatchVocabulary("I left the keys on the door.", forms, DefaultVocabularyShortlist); len(got) != 0 {
		t.Fatalf("shortlist = %v, want none", got)
	}
}

func TestMatchVocabulary_LoneWordKeepsPointer(t *testing.T) {
	forms := [][]string{entry("Endře"), entry("Mochala"), entry("Nemotron")}
	for transcript, heard := range map[string]string{
		"I had lunch with Andre yesterday and he loved the place.": "Andre",
		"Namotron runs on the second GPU now.":                     "Namotron",
	} {
		got := MatchVocabulary(transcript, forms, DefaultVocabularyShortlist)
		if len(got) != 1 || got[0].Heard != heard {
			t.Errorf("%q: shortlist = %+v, want one word pointing at %q", transcript, got, heard)
		}
	}
}

func TestMatchVocabulary_FullNameKeepsPointer(t *testing.T) {
	forms := [][]string{entry("Endře"), entry("Mochala")}
	got := MatchVocabulary("Please send the invoice to Andrea Mochala before Friday.", forms, DefaultVocabularyShortlist)
	heard := map[string]string{}
	for _, m := range got {
		heard[forms[m.Index][0]] = m.Heard
	}
	want := map[string]string{"Endře": "Andrea", "Mochala": ""}
	if !reflect.DeepEqual(heard, want) {
		t.Errorf("heard = %v, want %v", heard, want)
	}
}

func TestMatchVocabulary_TaughtVariantKeepsPointer(t *testing.T) {
	forms := [][]string{entry("Karlota", "Charlotte")}
	got := MatchVocabulary("I spoke with Charlotte today.", forms, DefaultVocabularyShortlist)
	if len(got) != 1 || got[0].Heard != "Charlotte" {
		t.Fatalf("shortlist = %+v, want Karlota pointing at Charlotte", got)
	}
}

func TestMatchVocabulary_MultiWordSpanKeepsPointer(t *testing.T) {
	forms := [][]string{entry("Hetzner")}
	got := MatchVocabulary("we moved the server to Head Sner last week", forms, DefaultVocabularyShortlist)
	if len(got) != 1 || got[0].Heard != "Head Sner" {
		t.Fatalf("shortlist = %+v, want Hetzner pointing at \"Head Sner\"", got)
	}
}

func TestRankVocabulary_MatchesMatchVocabularyOrder(t *testing.T) {
	forms := [][]string{entry("endre"), entry("fennec")}
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
		{"Mochala", nil, "McCullough", `Mochala (sounds like "McCullough" here)`},
		{"Diction", []string{"ditching", "diction app"}, "", "Diction (also heard as: ditching, diction app)"},
		{"Diction", []string{"ditching"}, "ditching", `Diction (also heard as: ditching; sounds like "ditching" here)`},
	}
	for _, tc := range cases {
		if got := CustomWordLine(tc.word, tc.variants, tc.heard); got != tc.want {
			t.Errorf("CustomWordLine(%q, %v, %q) = %q, want %q", tc.word, tc.variants, tc.heard, got, tc.want)
		}
	}
}
