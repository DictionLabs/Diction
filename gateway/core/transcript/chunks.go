package transcript

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

type TranscriptChunk struct {
	Text  string
	Break string
}

const (
	chunkPrevContext = 700
	chunkNextContext = 300
)

type chunkParagraph struct {
	brk   string
	sents []string
}

func ChunkTranscript(text string, target int) []TranscriptChunk {
	text = strings.TrimSpace(text)
	if target <= 0 || len(text) <= target {
		return []TranscriptChunk{{Text: text}}
	}
	var out []TranscriptChunk
	for _, p := range groupParagraphs(text) {
		if len(p.sents) == 0 {
			continue
		}
		out = append(out, packParagraph(p, target)...)
	}
	if len(out) == 0 {
		return []TranscriptChunk{{Text: text}}
	}
	out[0].Break = ""
	return out
}

func groupParagraphs(text string) []chunkParagraph {
	paras := []chunkParagraph{{}}
	for _, s := range splitSentences(text) {
		if brk, ok := standaloneBreak(s); ok {
			if len(paras[len(paras)-1].sents) == 0 && len(paras) > 1 {
				if len(brk) > len(paras[len(paras)-1].brk) {
					paras[len(paras)-1].brk = brk
				}
				continue
			}
			paras = append(paras, chunkParagraph{brk: brk})
			continue
		}
		paras[len(paras)-1].sents = append(paras[len(paras)-1].sents, s)
	}
	return paras
}

func packParagraph(p chunkParagraph, target int) []TranscriptChunk {
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
	if len(local) > 1 && len(local[len(local)-1]) < target/3 {
		tail := local[len(local)-1]
		local = local[:len(local)-1]
		local[len(local)-1] += " " + tail
	}
	var out []TranscriptChunk
	for i, c := range local {
		brk := " "
		if i == 0 {
			brk = p.brk
		}
		out = append(out, TranscriptChunk{Text: c, Break: brk})
	}
	return out
}

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

func standaloneBreak(s string) (string, bool) {
	words := spokenWords(s)
	if len(words) == 0 || len(words) > maxSpokenPhraseWords {
		return "", false
	}
	brk, ok := spokenBreakPhrases[strings.Join(words, " ")]
	return brk, ok
}

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
