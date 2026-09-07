package core

import (
	"encoding/json"
	"testing"
)

func TestIsConcreteLanguage(t *testing.T) {
	cases := []struct {
		name string
		lang string
		want bool
	}{
		{"lowercase code", "cs", true},
		{"uppercase code", "CS", true},
		{"region subtag", "pt-BR", true},
		{"empty", "", false},
		{"auto sentinel", "auto", false},
		{"auto sentinel mixed case", "Auto", false},
		{"whitespace only", "   ", false},
		{"too long to be a bare code", "englishlanguage", false},
		{"contains a space", "en glish", false},
		{"arbitrary text", "'; DROP TABLE users; --", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsConcreteLanguage(tc.lang); got != tc.want {
				t.Errorf("IsConcreteLanguage(%q) = %v, want %v", tc.lang, got, tc.want)
			}
		})
	}
}

func TestWithContextLanguage_EmptyBlob(t *testing.T) {
	got := WithContextLanguage("", "cs")
	var obj map[string]string
	if err := json.Unmarshal([]byte(got), &obj); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if obj["language"] != "cs" {
		t.Errorf("language = %q, want cs", obj["language"])
	}
}

func TestWithContextLanguage_PreservesExistingKeysAndOverrides(t *testing.T) {
	got := WithContextLanguage(`{"before":"hello ","language":"en"}`, "cs")
	var obj map[string]string
	if err := json.Unmarshal([]byte(got), &obj); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if obj["before"] != "hello " {
		t.Errorf("before = %q, want %q", obj["before"], "hello ")
	}
	if obj["language"] != "cs" {
		t.Errorf("language = %q, want cs (gateway-resolved value must override the client's)", obj["language"])
	}
}

func TestWithContextLanguage_NormalizesRegionAndCase(t *testing.T) {
	got := WithContextLanguage("{}", "PT-BR")
	var obj map[string]string
	if err := json.Unmarshal([]byte(got), &obj); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if obj["language"] != "pt" {
		t.Errorf("language = %q, want pt", obj["language"])
	}
}

func TestWithContextLanguage_NonConcretePassesThroughByteIdentical(t *testing.T) {
	cases := []string{"", "auto", `{"before":"x"}`}
	for _, contextJSON := range cases {
		for _, lang := range []string{"", "auto", "Auto"} {
			if got := WithContextLanguage(contextJSON, lang); got != contextJSON {
				t.Errorf("WithContextLanguage(%q, %q) = %q, want unchanged %q",
					contextJSON, lang, got, contextJSON)
			}
		}
	}
}

func TestWithContextLanguage_MalformedBlobPassesThroughUnchanged(t *testing.T) {
	malformed := `{"before":"unterminated`
	got := WithContextLanguage(malformed, "cs")
	if got != malformed {
		t.Errorf("WithContextLanguage(malformed, cs) = %q, want unchanged %q", got, malformed)
	}
}

func TestWithContextLanguage_NonObjectJSONPassesThroughUnchanged(t *testing.T) {
	arr := `["not", "an", "object"]`
	got := WithContextLanguage(arr, "cs")
	if got != arr {
		t.Errorf("WithContextLanguage(array, cs) = %q, want unchanged %q", got, arr)
	}
}
