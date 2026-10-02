package core

import (
	"reflect"
	"testing"
)

func TestSanitizeSessionContext(t *testing.T) {
	names := [][]string{{"Ondrej"}, {"Machala"}}
	cases := []struct {
		name       string
		transcript string
		session    []string
		forms      [][]string
		want       []string
	}{
		{
			name:       "same mishearing in session is rewritten",
			transcript: "Hello, my name is Andre McCullough.",
			session:    []string{"Hello, my name is Andre McCullough."},
			forms:      names,
			want:       []string{"Hello, my name is Ondrej Machala."},
		},
		{
			name:       "session line holds a different mishearing than the utterance",
			transcript: "Hey, my name is Andre Makala.",
			session:    []string{"Hello, my name is Andre McCullough.", "Hey, my name is Andre Makala."},
			forms:      names,
			want:       []string{"Hello, my name is Ondrej Machala.", "Hey, my name is Ondrej Machala."},
		},
		{
			name:       "session line is matched on its own even when the utterance has no name",
			transcript: "Thanks, see you tomorrow.",
			session:    []string{"This is Andre McCullough speaking."},
			forms:      names,
			want:       []string{"This is Ondrej Machala speaking."},
		},
		{
			name:       "every occurrence, any case, punctuation kept",
			transcript: "andre said hi",
			session:    []string{"ANDRE, andre and Andre's friend."},
			forms:      [][]string{{"Ondrej"}},
			want:       []string{"Ondrej, Ondrej and Andre's friend."},
		},
		{
			name:       "ordinary words that sound like a name are left alone",
			transcript: "Leave it on the door.",
			session:    []string{"I left the keys on the door."},
			forms:      names,
			want:       []string{"I left the keys on the door."},
		},
		{
			name:       "canonical spelling and unrelated context untouched",
			transcript: "Ondrej Machala here.",
			session:    []string{"We deployed Kubernetes today.", "Ondrej Machala wrote this."},
			forms:      names,
			want:       []string{"We deployed Kubernetes today.", "Ondrej Machala wrote this."},
		},
		{
			name:       "multi-word mishearing",
			transcript: "the cube netties pod crashed",
			session:    []string{"Our cube netties cluster is up."},
			forms:      [][]string{{"Kubernetes", "cube netties"}},
			want:       []string{"Our Kubernetes cluster is up."},
		},
		{
			name:       "a span claimed by two custom words drops the line",
			transcript: "I spoke with Cristina.",
			session:    []string{"Cristina called.", "The weather is nice."},
			forms:      [][]string{{"Kristýna"}, {"Christina"}},
			want:       []string{"The weather is nice."},
		},
		{
			name:       "no custom words: session passes through",
			transcript: "Andre McCullough",
			session:    []string{"Andre McCullough"},
			forms:      nil,
			want:       []string{"Andre McCullough"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := append([]string(nil), tc.session...)
			got := SanitizeSessionContext(tc.transcript, tc.session, tc.forms)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
			if !reflect.DeepEqual(in, tc.session) {
				t.Errorf("input mutated: %q", tc.session)
			}
		})
	}
}

func TestSanitizeSessionContextEmpty(t *testing.T) {
	if got := SanitizeSessionContext("x", nil, [][]string{{"Ondrej"}}); got != nil {
		t.Errorf("nil session: got %q", got)
	}
}
