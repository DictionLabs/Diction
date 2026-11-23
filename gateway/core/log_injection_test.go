package core

import (
	"bytes"
	"log"
	"os"
	"testing"
)

func captureCoreLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(os.Stderr); log.SetFlags(log.LstdFlags) })
	return &buf
}

func TestSTTDemotionTTLLogLineNormalValueUnchanged(t *testing.T) {
	buf := captureCoreLog(t)
	t.Setenv("DICTION_STT_DEMOTION_TTL", "soon")
	sttDemotionTTLFromEnv()
	want := "DICTION_STT_DEMOTION_TTL=\"soon\" unparsable — using 2m0s\n"
	if buf.String() != want {
		t.Fatalf("got %q want %q", buf.String(), want)
	}
}

func TestSTTDemotionTTLLogLineCannotBeForged(t *testing.T) {
	buf := captureCoreLog(t)
	t.Setenv("DICTION_STT_DEMOTION_TTL", "x\nFAKE LINE: admin logged in")
	sttDemotionTTLFromEnv()
	if got := bytes.Count(buf.Bytes(), []byte("\n")); got != 1 {
		t.Fatalf("a newline in the env value split the log line: %q", buf.String())
	}
}
