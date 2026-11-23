package core

import (
	"strings"
	"testing"
)

func TestBuildCleanupUserMessage_NeighbourParts(t *testing.T) {
	msg, _ := BuildCleanupUserMessage(CleanupMessage{Transcript: "b", PreviousPart: "a", NextPart: "c", Closing: CleanupClosing})
	want := "<transcript>b</transcript>\n<previous_part>a</previous_part>\n<next_part>c</next_part>\n\n" + CleanupClosing
	if msg != want {
		t.Errorf("got %q", msg)
	}
	if msg, _ := BuildCleanupUserMessage(CleanupMessage{Transcript: "b"}); strings.Contains(msg, "_part>") {
		t.Errorf("neighbour tags without neighbours: %q", msg)
	}
}
