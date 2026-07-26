package core

import (
	"encoding/json"
	"os"
	"testing"
)

// fixturePath is relative to this package (gateway/core) — the shared
// fixture lives at repo-root fixtures/, sibling to gateway/ and ios/, so
// both the Go and Swift test suites verify against one canonical spec.
const fixturePath = "../../fixtures/transcript-cleanup.json"

type cleanupFixture struct {
	Cases []struct {
		ID            string `json:"id"`
		Language      string `json:"language"`
		Input         string `json:"input"`
		ExpectedLight string `json:"expected_light"`
		ExpectedFull  string `json:"expected_full"`
	} `json:"cases"`
}

func loadCleanupFixture(t *testing.T) cleanupFixture {
	t.Helper()
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", fixturePath, err)
	}
	var fx cleanupFixture
	if err := json.Unmarshal(data, &fx); err != nil {
		t.Fatalf("failed to parse fixture: %v", err)
	}
	return fx
}

func TestCleanTranscript_Fixtures(t *testing.T) {
	fx := loadCleanupFixture(t)
	for _, c := range fx.Cases {
		t.Run(c.ID+"/whisper", func(t *testing.T) {
			got, _ := CleanTranscript(c.Input, c.Language, "whisper")
			if got != c.ExpectedLight {
				t.Errorf("CleanTranscript(%q, %q, whisper) = %q, want %q", c.Input, c.Language, got, c.ExpectedLight)
			}
		})
		t.Run(c.ID+"/parakeet", func(t *testing.T) {
			got, _ := CleanTranscript(c.Input, c.Language, "parakeet")
			if got != c.ExpectedFull {
				t.Errorf("CleanTranscript(%q, %q, parakeet) = %q, want %q", c.Input, c.Language, got, c.ExpectedFull)
			}
		})
		t.Run(c.ID+"/canary", func(t *testing.T) {
			got, _ := CleanTranscript(c.Input, c.Language, "canary")
			if got != c.ExpectedFull {
				t.Errorf("CleanTranscript(%q, %q, canary) = %q, want %q", c.Input, c.Language, got, c.ExpectedFull)
			}
		})
	}
}

func TestCleanTranscript_Identity(t *testing.T) {
	if got, changed := CleanTranscript("", "en", "parakeet"); got != "" || changed {
		t.Errorf("CleanTranscript(empty) = %q, changed=%v, want empty, false", got, changed)
	}
	text := "hello world"
	if got, changed := CleanTranscript(text, "en", "parakeet"); got != text || changed {
		t.Errorf("CleanTranscript(%q) = %q, changed=%v, want unchanged", text, got, changed)
	}
}

func TestCleanTranscript_ChangedFlag(t *testing.T) {
	got, changed := CleanTranscript("so um I think uh we should go", "en", "parakeet")
	if !changed {
		t.Errorf("expected changed=true, got cleaned=%q changed=%v", got, changed)
	}
}
