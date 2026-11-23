package core

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
)

const goldenPath = "testdata/proxy_golden.txt"

func currentTranscriptionHandler(g *Gateway, pp func(context.Context, string, string, string) (string, string, error)) http.HandlerFunc {
	return g.TranscriptionHandlerWithPostProcess(pp)
}

func renderGolden(t *testing.T, factory func(g *Gateway, pp func(context.Context, string, string, string) (string, string, error)) http.HandlerFunc) string {
	var sb strings.Builder
	for _, sc := range goldScenarios(t) {
		sb.WriteString("=== " + sc.name + "\n")
		sb.WriteString(runGoldScenario(t, sc, factory))
	}
	return sb.String()
}

func TestTranscriptionHandler_GoldenBehaviour(t *testing.T) {
	got := renderGolden(t, currentTranscriptionHandler)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		gl, wl := strings.Split(got, "\n"), strings.Split(string(want), "\n")
		for i := 0; i < len(gl) && i < len(wl); i++ {
			if gl[i] != wl[i] {
				t.Fatalf("golden mismatch at line %d:\n got: %s\nwant: %s", i+1, gl[i], wl[i])
			}
		}
		t.Fatalf("golden length mismatch: got %d lines, want %d", len(gl), len(wl))
	}
}
