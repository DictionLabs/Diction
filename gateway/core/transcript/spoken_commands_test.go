package transcript

import "testing"

func TestApplySpokenTrailingBreak(t *testing.T) {
	tests := []struct {
		name       string
		transcript string
		cleaned    string
		after      string
		want       string
	}{
		{"paragraph at the end", "see you tomorrow new paragraph", "See you tomorrow.", "", "See you tomorrow.\n\n"},
		{"line at the end", "call the plumber new line", "Call the plumber.", "", "Call the plumber.\n"},
		{"punctuated STT form", "See you tomorrow. New paragraph.", "See you tomorrow.", "", "See you tomorrow.\n\n"},
		{"comma-wrapped STT form", "See you tomorrow, new line,", "See you tomorrow.", "", "See you tomorrow.\n"},
		{"trap: model kept the words", "I added a new paragraph", "I added a new paragraph.", "", "I added a new paragraph."},
		{"no command", "see you tomorrow", "See you tomorrow.", "", "See you tomorrow."},
		{"command only mid-text", "hi new paragraph thanks", "Hi\n\nThanks.", "", "Hi\n\nThanks."},
		{"next paragraph is not a command", "see the next paragraph", "See the next paragraph.", "", "See the next paragraph."},
		{"German", "vielen Dank neuer Absatz", "Vielen Dank.", "", "Vielen Dank.\n\n"},
		{"French", "merci nouveau paragraphe", "Merci.", "", "Merci.\n\n"},
		{"Czech", "díky nový odstavec", "Díky.", "", "Díky.\n\n"},
		{"Italian two-word line", "grazie a capo", "Grazie.", "", "Grazie.\n"},
		{"after already starts with a newline", "see you new paragraph", "See you.", "\nNext", "See you.\n"},
		{"after already starts with a blank line", "see you new paragraph", "See you.", "\n\nNext", "See you."},
		{"line when after starts with a newline", "see you new line", "See you.", "\nNext", "See you."},
		{"after with a word keeps the break", "see you new paragraph", "See you.", "Next", "See you.\n\n"},
		{"CRLF normalised", "a new line b", "A.\r\nB.", "", "A.\nB."},
		{"lone CR normalised", "a new line b", "A.\rB.", "", "A.\nB."},
		{"transcript is only the command", "New paragraph.", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ApplySpokenTrailingBreak(tt.transcript, tt.cleaned, tt.after); got != tt.want {
				t.Errorf("ApplySpokenTrailingBreak(%q, %q, %q) = %q, want %q", tt.transcript, tt.cleaned, tt.after, got, tt.want)
			}
		})
	}
}

func TestSpokenLeadingBreak(t *testing.T) {
	tests := []struct {
		name       string
		transcript string
		cleaned    string
		want       string
	}{
		{"paragraph at the start", "new paragraph thanks for sending the file", "Thanks for sending the file.", "\n\n"},
		{"line at the start", "new line call the plumber", "Call the plumber.", "\n"},
		{"punctuated STT form", "New paragraph. Thanks for the file.", "Thanks for the file.", "\n\n"},
		{"German", "neuer Absatz vielen Dank", "Vielen Dank.", "\n\n"},
		{"French three-word line", "à la ligne merci beaucoup", "Merci beaucoup.", "\n"},
		{"trap: new line of credit kept as words", "new line of credit approved", "New line of credit approved.", ""},
		{"cleaned still starts with the phrase", "new paragraph thanks", "New paragraph, thanks.", ""},
		{"no command", "thanks for the file", "Thanks for the file.", ""},
		{"command only at the end", "see you new paragraph", "See you.", ""},
		{"next paragraph is not a command", "next paragraph is about cost", "Next paragraph is about cost.", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SpokenLeadingBreak(tt.transcript, tt.cleaned); got != tt.want {
				t.Errorf("SpokenLeadingBreak(%q, %q) = %q, want %q", tt.transcript, tt.cleaned, got, tt.want)
			}
		})
	}
}

func TestApplySpokenLeadingBreak_dedupsAgainstBefore(t *testing.T) {
	const transcript = "new paragraph thanks"
	tests := []struct {
		name    string
		cleaned string
		before  string
		want    string
	}{
		{"empty before gets the whole break", "Thanks.", "", "\n\nThanks."},
		{"before mid-sentence", "Thanks.", "Hi,", "\n\nThanks."},
		{"before ends with one newline", "Thanks.", "Hi,\n", "\nThanks."},
		{"before ends with a blank line", "Thanks.", "Hi,\n\n", "Thanks."},
		{"trailing spaces after the newline are skipped", "Thanks.", "Hi,\n\n  ", "Thanks."},
		{"CRLF before normalised", "Thanks.", "Hi,\r\n", "\nThanks."},
		{"CRLF in cleaned normalised", "Thanks.\r\nBye.", "", "\n\nThanks.\nBye."},
		{"leading newlines of cleaned are not doubled", "\nThanks.", "", "\n\nThanks."},
		{"blank cleaned untouched", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ApplySpokenLeadingBreak(transcript, tt.cleaned, tt.before); got != tt.want {
				t.Errorf("ApplySpokenLeadingBreak(%q, %q, %q) = %q, want %q", transcript, tt.cleaned, tt.before, got, tt.want)
			}
		})
	}
	if got := ApplySpokenLeadingBreak("thanks", "Thanks.", ""); got != "Thanks." {
		t.Errorf("no command: got %q", got)
	}
}

func TestApplySpokenBreakOnly(t *testing.T) {
	tests := []struct {
		name, transcript, cleaned, before, after string
		want                                     string
		wantOK                                   bool
	}{
		{"paragraph only, blank cleanup", "New paragraph.", "", "Hi there.", "", "\n\n", true},
		{"line only, blank cleanup", "new line", "", "Hi there.", "", "\n", true},
		{"model kept the words", "New paragraph.", "New paragraph.", "Hi.", "", "\n\n", true},
		{"two lines cap at one blank line", "new line new line new line", "", "Hi.", "", "\n\n", true},
		{"German", "Neuer Absatz.", "", "Hallo.", "", "\n\n", true},
		{"before already ends with a newline", "new paragraph", "", "Hi.\n", "", "\n", true},
		{"before ends with a blank line and spaces", "new paragraph", "", "Hi.\n\n  ", "", "", true},
		{"after starts with a newline", "new paragraph", "", "Hi.", "\nNext", "\n", true},
		{"empty field still gets the break", "new paragraph", "", "", "", "\n\n", true},
		{"truly empty transcript", "", "", "Hi.", "", "", false},
		{"punctuation-only transcript", " . ", "", "Hi.", "", "", false},
		{"command plus content is not break-only", "thanks new paragraph", "Thanks.", "", "", "", false},
		{"blank cleanup of other words untouched", "um", "", "", "", "", false},
		{"break-only transcript but cleanup has content", "new line", "New line of credit.", "", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ApplySpokenBreakOnly(tt.transcript, tt.cleaned, tt.before, tt.after)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("ApplySpokenBreakOnly(%q, %q, %q, %q) = %q, %v; want %q, %v",
					tt.transcript, tt.cleaned, tt.before, tt.after, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestSpokenCommandWordCount(t *testing.T) {
	tests := []struct {
		raw  string
		want int
	}{
		{"see you tomorrow", 0},
		{"hi new paragraph thanks", 2},
		{"Hi team. New paragraph. The meeting is at five. New paragraph. Thanks.", 4},
		{"hallo neuer Absatz danke", 2},
		{"dear Tom colon new paragraph the invoice is attached period", 4},
		{"a question mark and a full stop", 4},
		{"we launch a new line of shoes", 2},
		{"see the next paragraph", 0},
	}
	for _, tt := range tests {
		if got := SpokenCommandWordCount(tt.raw); got != tt.want {
			t.Errorf("SpokenCommandWordCount(%q) = %d, want %d", tt.raw, got, tt.want)
		}
	}
}
