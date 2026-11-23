package core

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
)

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(os.Stderr); log.SetFlags(log.LstdFlags) })
	return &buf
}

func TestLogOpenAuthWarningSilentWhenKeyRequired(t *testing.T) {
	buf := captureLog(t)
	for _, mode := range []string{"required", ""} {
		for _, p := range []PairingState{PairingReady, PairingDisabled, PairingNone} {
			if LogOpenAuthWarning(mode, p) || buf.Len() != 0 {
				t.Errorf("mode %q pairing %v must not warn, logged %q", mode, p, buf.String())
			}
		}
	}
}

func TestLogOpenAuthWarningWording(t *testing.T) {
	cases := []struct {
		name    string
		mode    string
		pairing PairingState
		want    []string
		not     []string
	}{
		{"optional ready", "optional", PairingReady,
			[]string{"DICTION_GATEWAY_AUTH=optional", "DICTION_GATEWAY_AUTH=required", "gateway auth"}, []string{"DICTION_KEY_PATH"}},
		{"off ready", "off", PairingReady,
			[]string{"DICTION_GATEWAY_AUTH=off", "DICTION_GATEWAY_AUTH=required", "gateway auth"}, []string{"DICTION_KEY_PATH", "optional is the default"}},
		{"optional disabled", "optional", PairingDisabled,
			[]string{"DICTION_GATEWAY_AUTH=optional", "DICTION_KEY_PATH", "Pairing is disabled"}, []string{"gateway auth"}},
		{"off disabled", "off", PairingDisabled,
			[]string{"DICTION_GATEWAY_AUTH=off", "DICTION_KEY_PATH", "Pairing is disabled"}, []string{"gateway auth"}},
		{"optional no pairing", "optional", PairingNone,
			[]string{"DICTION_GATEWAY_AUTH=optional"}, []string{"gateway auth", "DICTION_KEY_PATH", "pair"}},
		{"off no pairing", "off", PairingNone,
			[]string{"DICTION_GATEWAY_AUTH=off"}, []string{"gateway auth", "DICTION_KEY_PATH", "pair"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			buf := captureLog(t)
			if !LogOpenAuthWarning(c.mode, c.pairing) {
				t.Fatal("must warn")
			}
			out := buf.String()
			if strings.Count(out, "\n") != 1 {
				t.Errorf("want exactly one line, got %q", out)
			}
			for _, w := range append([]string{"WARNING", "unauthenticated transcription requests"}, c.want...) {
				if !strings.Contains(out, w) {
					t.Errorf("lacks %q: %s", w, out)
				}
			}
			for _, n := range c.not {
				if strings.Contains(out, n) {
					t.Errorf("must not contain %q: %s", n, out)
				}
			}
		})
	}
}
