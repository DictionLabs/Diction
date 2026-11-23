package core

import (
	"os/exec"
	"strings"
	"testing"
)

func TestLimitedStderrWriter_ReportsFullWritePastCap(t *testing.T) {
	lw := &limitedStderrWriter{cap: 10}
	for _, chunk := range []string{"123456", "7890abcdef", "more"} {
		if n, err := lw.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("Write(%q) = %d, %v; want %d, nil", chunk, n, err, len(chunk))
		}
	}
	if got := lw.buf.String(); got != "1234567890" {
		t.Errorf("buffer = %q, want the first 10 bytes", got)
	}
}

func TestLimitedStderrWriter_ChildSurvivesNoisyStderr(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	lw := &limitedStderrWriter{cap: 4096}
	cmd := exec.Command("sh", "-c", `i=0; while [ $i -lt 250 ]; do printf '%079d\n' 0 >&2; i=$((i+1)); done`)
	cmd.Stderr = lw
	if err := cmd.Run(); err != nil {
		t.Fatalf("child failed with noisy stderr: %v", err)
	}
	if lw.buf.Len() != 4096 || !strings.HasPrefix(lw.buf.String(), "0000") {
		t.Errorf("captured %d bytes, want exactly the 4096-byte cap", lw.buf.Len())
	}
}
