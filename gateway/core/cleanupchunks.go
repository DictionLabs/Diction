package core

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Chunked Clean up: a long dictation is split at sentence boundaries and its parts are cleaned
// in parallel, each seeing its raw neighbours as read-only context (CleanupMessage.PreviousPart /
// NextPart, prompts.CleanupChunkSystemPrompt). gpt-oss writes a medium transcript out in its
// hidden reasoning before answering, so a long one cost reasoning + output tokens in series;
// parallel parts cost the slowest part. Measured 2026-10-18 on 14 long dictations (en/cs/de/es):
// mean 2.12 → 1.14 s, and blind Claude judges preferred the chunked text 15-10 against a
// production-vs-production noise floor of 10-9 (.claude/CLEANUP_LATENCY_RESEARCH.md).
//
// The seams are where it breaks, so they are chosen: a recogniser writes a spoken "new
// paragraph" as a sentence of its own, which is the safest place to cut, and the command
// becomes the join. Otherwise parts never split a sentence, and joins keep list items on
// consecutive lines.

// TranscriptChunk is one part of a split transcript. Break is what joins it to the part before
// ("" for the first): " " inside a paragraph, or the break of a spoken command consumed at
// the seam.
type TranscriptChunk struct {
	Text  string
	Break string
}

// Neighbour windows: enough text before a part for what it refers back to (a name, a list in
// progress, a correction's target), a little after for where a sentence is heading.
const (
	chunkPrevContext = 700
	chunkNextContext = 300
)

// ChunkTranscript splits text into parts of about target bytes. It returns a single chunk when
// the text does not split (no sentence boundary, or shorter than target).
func ChunkTranscript(text string, target int) []TranscriptChunk {
	text = strings.TrimSpace(text)
	if target <= 0 || len(text) <= target {
		return []TranscriptChunk{{Text: text}}
	}
	// Paragraphs, split at sentences that are only a spoken break command.
	type paragraph struct {
		brk   string // break before this paragraph
		sents []string
	}
	paras := []paragraph{{}}
	for _, s := range splitSentences(text) {
		if brk, ok := standaloneBreak(s); ok {
			if len(paras[len(paras)-1].sents) == 0 && len(paras) > 1 {
				// two commands in a row: keep the bigger break
				if len(brk) > len(paras[len(paras)-1].brk) {
					paras[len(paras)-1].brk = brk
				}
				continue
			}
			paras = append(paras, paragraph{brk: brk})
			continue
		}
		paras[len(paras)-1].sents = append(paras[len(paras)-1].sents, s)
	}

	var out []TranscriptChunk
	for _, p := range paras {
		if len(p.sents) == 0 {
			continue // a leading or trailing command: finishTranscriptText restores edge breaks
		}
		var local []string
		var cur []string
		n := 0
		for _, s := range p.sents {
			if len(cur) > 0 && n+1+len(s) > target {
				local = append(local, strings.Join(cur, " "))
				cur, n = nil, 0
			}
			cur = append(cur, s)
			n += len(s) + 1
		}
		if len(cur) > 0 {
			local = append(local, strings.Join(cur, " "))
		}
		// A short tail joins the part before it rather than being cleaned almost alone.
		if len(local) > 1 && len(local[len(local)-1]) < target/3 {
			tail := local[len(local)-1]
			local = local[:len(local)-1]
			local[len(local)-1] += " " + tail
		}
		for i, c := range local {
			brk := " "
			if i == 0 {
				brk = p.brk
			}
			out = append(out, TranscriptChunk{Text: c, Break: brk})
		}
	}
	if len(out) == 0 {
		return []TranscriptChunk{{Text: text}}
	}
	out[0].Break = ""
	return out
}

// splitSentences splits after . ! ? … followed by whitespace, and at line breaks.
func splitSentences(text string) []string {
	var out []string
	start := 0
	for i, r := range text {
		if r == '\n' {
			if s := strings.TrimSpace(text[start:i]); s != "" {
				out = append(out, s)
			}
			start = i + 1
			continue
		}
		if !strings.ContainsRune(".!?…", r) {
			continue
		}
		next := i + utf8.RuneLen(r)
		if next < len(text) {
			if nr, _ := utf8.DecodeRuneInString(text[next:]); !unicode.IsSpace(nr) {
				continue
			}
		}
		if s := strings.TrimSpace(text[start:next]); s != "" {
			out = append(out, s)
		}
		start = next
	}
	if s := strings.TrimSpace(text[start:]); s != "" {
		out = append(out, s)
	}
	return out
}

// standaloneBreak reports whether sentence s is nothing but a spoken break command ("New
// paragraph.", "nový odstavec"), and the break it stands for.
func standaloneBreak(s string) (string, bool) {
	words := spokenWords(s)
	if len(words) == 0 || len(words) > maxSpokenPhraseWords {
		return "", false
	}
	brk, ok := spokenBreakPhrases[strings.Join(words, " ")]
	return brk, ok
}

// ChunkNeighbours returns the raw text around part i: up to chunkPrevContext bytes before it
// and chunkNextContext after it, cut at a word boundary.
func ChunkNeighbours(chunks []TranscriptChunk, i int) (prev, next string) {
	var b strings.Builder
	for j := 0; j < i; j++ {
		if j > 0 {
			b.WriteString(" ")
		}
		b.WriteString(chunks[j].Text)
	}
	prev = b.String()
	if len(prev) > chunkPrevContext {
		prev = prev[len(prev)-chunkPrevContext:]
		if k := strings.IndexByte(prev, ' '); k >= 0 {
			prev = prev[k+1:]
		}
	}
	b.Reset()
	for j := i + 1; j < len(chunks); j++ {
		if b.Len() > 0 {
			b.WriteString(" ")
		}
		b.WriteString(chunks[j].Text)
		if b.Len() >= chunkNextContext {
			break
		}
	}
	next = b.String()
	if len(next) > chunkNextContext {
		next = next[:chunkNextContext]
		if k := strings.LastIndexByte(next, ' '); k >= 0 {
			next = next[:k]
		}
	}
	return prev, next
}

var listItemLine = regexp.MustCompile(`^\s*(?:[-•]|\d+[.)])\s`)

// JoinCleanedChunks joins the cleaned parts (cleaned[i] for chunks[i]). A command break is
// kept as is; inside a paragraph two list items join on the next line, a list next to prose
// gets a blank line, and prose joins with a space.
func JoinCleanedChunks(chunks []TranscriptChunk, cleaned []string) string {
	var b strings.Builder
	for i, c := range cleaned {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if b.Len() > 0 {
			sep := chunks[i].Break
			if sep == " " || sep == "" {
				acc := strings.TrimRight(b.String(), " ")
				last := acc[strings.LastIndexByte(acc, '\n')+1:]
				first := c
				if k := strings.IndexByte(c, '\n'); k >= 0 {
					first = c[:k]
				}
				lastItem, firstItem := listItemLine.MatchString(last), listItemLine.MatchString(first)
				switch {
				case lastItem && firstItem:
					sep = "\n"
				case lastItem || firstItem:
					sep = "\n\n"
				default:
					sep = " "
				}
			}
			b.WriteString(sep)
		}
		b.WriteString(c)
	}
	return ConsistentApostrophes(b.String())
}

// ConsistentApostrophes makes in-word apostrophes one style when a text mixes ’ and ' ("don’t"
// next to "didn't"): parts cleaned separately pick their own. The majority wins; quotes are
// left alone.
func ConsistentApostrophes(s string) string {
	curly, straight := 0, 0
	rs := []rune(s)
	inWord := func(i int) bool {
		return i > 0 && i < len(rs)-1 && unicode.IsLetter(rs[i-1]) && unicode.IsLetter(rs[i+1])
	}
	for i, r := range rs {
		if (r == '’' || r == '\'') && inWord(i) {
			if r == '’' {
				curly++
			} else {
				straight++
			}
		}
	}
	if curly == 0 || straight == 0 {
		return s
	}
	from, to := '’', '\''
	if curly > straight {
		from, to = '\'', '’'
	}
	for i, r := range rs {
		if r == from && inWord(i) {
			rs[i] = to
		}
	}
	return string(rs)
}
