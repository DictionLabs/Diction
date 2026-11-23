package core

import (
	"bytes"
	"testing"
)

func TestOggDurationMs_TopBitGranuleIsZeroNotEarlierPage(t *testing.T) {
	var buf bytes.Buffer
	writeOggPage(&buf, 0x02, 0, 0, 0, []byte("OpusHead\x01\x01\x00\x00"))
	writeOggPage(&buf, 0x00, 48000, 1, 0, []byte("early-page"))
	writeOggPage(&buf, 0x00, -1<<63+1, 2, 0, []byte("late-page"))
	if got := oggDurationMs(buf.Bytes()); got != 0 {
		t.Fatalf("0x8000000000000001 on the last page: want 0 ms, got %d", got)
	}

	var skipped bytes.Buffer
	writeOggPage(&skipped, 0x02, 0, 0, 0, []byte("OpusHead\x01\x01\x00\x00"))
	writeOggPage(&skipped, 0x00, 48000, 1, 0, []byte("early-page"))
	writeOggPage(&skipped, 0x00, -1, 2, 0, []byte("late-page"))
	if got := oggDurationMs(skipped.Bytes()); got != 1000 {
		t.Fatalf("all-ones granule is skipped: want 1000 ms from the earlier page, got %d", got)
	}
}
