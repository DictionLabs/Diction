package core

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
)

func TestLogOpenAuthWarning(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(os.Stderr); log.SetFlags(log.LstdFlags) })

	for _, mode := range []string{"required", "off", ""} {
		if LogOpenAuthWarning(mode) || buf.Len() != 0 {
			t.Errorf("mode %q must not warn, logged %q", mode, buf.String())
		}
	}
	if !LogOpenAuthWarning("optional") {
		t.Fatal("optional mode must warn")
	}
	out := buf.String()
	if strings.Count(out, "\n") != 1 {
		t.Errorf("want exactly one line, got %q", out)
	}
	for _, want := range []string{"WARNING", "unauthenticated transcription requests", "DICTION_GATEWAY_AUTH=required", "gateway auth"} {
		if !strings.Contains(out, want) {
			t.Errorf("warning lacks %q: %s", want, out)
		}
	}
}
