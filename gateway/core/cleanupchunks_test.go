package core

import (
	"strings"
	"testing"
)

func chunkTexts(cs []TranscriptChunk) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Text
	}
	return out
}

func TestChunkTranscript_ShortIsOneChunk(t *testing.T) {
	cs := ChunkTranscript("  Hello there. How are you?  ", 500)
	if len(cs) != 1 || cs[0].Text != "Hello there. How are you?" || cs[0].Break != "" {
		t.Fatalf("got %+v", cs)
	}
	if cs := ChunkTranscript(strings.Repeat("word ", 300), 100); len(cs) != 1 {
		t.Errorf("no sentence boundary: want one chunk, got %d", len(cs))
	}
}

func TestChunkTranscript_KeepsEveryWordInOrder(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 40; i++ {
		sb.WriteString("This is sentence number ")
		sb.WriteString(strings.Repeat("x", i%7))
		sb.WriteString(" in the dictation. ")
	}
	text := strings.TrimSpace(sb.String())
	cs := ChunkTranscript(text, 200)
	if len(cs) < 3 {
		t.Fatalf("want several chunks, got %d", len(cs))
	}
	if got := strings.Join(chunkTexts(cs), " "); got != text {
		t.Errorf("chunks do not reassemble the transcript")
	}
	for i, c := range cs {
		if i > 0 && c.Break != " " {
			t.Errorf("chunk %d break %q, want space", i, c.Break)
		}
		if !strings.HasSuffix(c.Text, ".") {
			t.Errorf("chunk %d does not end at a sentence boundary: %q", i, c.Text)
		}
	}
}

// The short-tail merge must append to the LAST part. The prototype indexed [-2] before popping
// and overwrote the first part: opening lost, middle repeated.
func TestChunkTranscript_ShortTailJoinsLastPart(t *testing.T) {
	text := strings.Repeat("A", 90) + ". " + strings.Repeat("B", 90) + ". C."
	cs := ChunkTranscript(text, 100)
	if len(cs) != 2 || !strings.HasPrefix(cs[0].Text, "AAA") || cs[1].Text != strings.Repeat("B", 90)+". C." {
		t.Fatalf("got %q", chunkTexts(cs))
	}
}

func TestChunkTranscript_SplitsAtSpokenParagraph(t *testing.T) {
	text := "Hi Anna. New paragraph. Thanks for the file, it was good. " + strings.Repeat("More words here. ", 20) + "Nový odstavec. See you."
	cs := ChunkTranscript(text, 120)
	if cs[0].Text != "Hi Anna." {
		t.Fatalf("first chunk %q", cs[0].Text)
	}
	if cs[1].Break != "\n\n" || !strings.HasPrefix(cs[1].Text, "Thanks") {
		t.Errorf("second chunk %+v", cs[1])
	}
	last := cs[len(cs)-1]
	if last.Text != "See you." || last.Break != "\n\n" {
		t.Errorf("last chunk %+v", last)
	}
	for _, c := range cs {
		if strings.Contains(strings.ToLower(c.Text), "paragraph") || strings.Contains(c.Text, "odstavec") {
			t.Errorf("command left in chunk %q", c.Text)
		}
	}
}

func TestChunkTranscript_InSentenceCommandStays(t *testing.T) {
	// "Hi Anna, new paragraph." is not a sentence of only the command: the prompt handles it.
	text := "Hi Anna, new paragraph. " + strings.Repeat("Thanks for the file. ", 10)
	cs := ChunkTranscript(text, 80)
	if !strings.HasPrefix(cs[0].Text, "Hi Anna, new paragraph.") {
		t.Errorf("got %q", cs[0].Text)
	}
}

func TestChunkNeighbours(t *testing.T) {
	cs := []TranscriptChunk{{Text: "One two."}, {Text: "Three four.", Break: " "}, {Text: "Five six.", Break: " "}}
	prev, next := ChunkNeighbours(cs, 1)
	if prev != "One two." || next != "Five six." {
		t.Errorf("prev %q next %q", prev, next)
	}
	prev, next = ChunkNeighbours(cs, 0)
	if prev != "" || next != "Three four. Five six." {
		t.Errorf("first: prev %q next %q", prev, next)
	}
	long := []TranscriptChunk{{Text: strings.Repeat("word ", 400)}, {Text: "x", Break: " "}}
	if prev, _ := ChunkNeighbours(long, 1); len(prev) > chunkPrevContext || strings.HasPrefix(prev, "ord") {
		t.Errorf("prev window not cut at a word: %d %q", len(prev), prev[:10])
	}
}

func TestJoinCleanedChunks(t *testing.T) {
	cs := []TranscriptChunk{{}, {Break: "\n\n"}, {Break: " "}, {Break: " "}, {Break: " "}}
	got := JoinCleanedChunks(cs, []string{"Hi Anna,", " Thanks. ", "1. Milk\n2. Eggs", "3. Bread", "That's all."})
	want := "Hi Anna,\n\nThanks.\n\n1. Milk\n2. Eggs\n3. Bread\n\nThat's all."
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if got := JoinCleanedChunks(cs[:2], []string{"", "Only."}); got != "Only." {
		t.Errorf("empty part: %q", got)
	}
}

func TestConsistentApostrophes(t *testing.T) {
	if got := ConsistentApostrophes("I don’t know, we didn't, they can't."); got != "I don't know, we didn't, they can't." {
		t.Errorf("got %q", got)
	}
	if got := ConsistentApostrophes("It’s ‘quoted’ and it’s fine, isn't it?"); got != "It’s ‘quoted’ and it’s fine, isn’t it?" {
		t.Errorf("got %q", got)
	}
	if got := ConsistentApostrophes("'Hello', I don't"); got != "'Hello', I don't" {
		t.Errorf("quotes changed: %q", got)
	}
}

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
