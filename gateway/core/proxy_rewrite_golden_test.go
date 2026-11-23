package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"strings"
	"testing"
)

const rewriteGoldenPath = "testdata/rewrite_multipart_golden.txt"

type rewriteCase struct {
	name string
	body func(t *testing.T) []byte
	opts MultipartRewriteOpts
}

func rawMultipart(parts ...string) []byte {
	var sb strings.Builder
	for _, p := range parts {
		sb.WriteString("--BND\r\n" + p + "\r\n")
	}
	sb.WriteString("--BND--\r\n")
	return []byte(sb.String())
}

func fieldPart(name, value string) string {
	return fmt.Sprintf("Content-Disposition: form-data; name=%q\r\n\r\n%s", name, value)
}

func filePart(name, filename, ctype, data string) string {
	return fmt.Sprintf("Content-Disposition: form-data; name=%q; filename=%q\r\nContent-Type: %s\r\n\r\n%s", name, filename, ctype, data)
}

func describeRewritten(body []byte, ct string) string {
	_, params, _ := mime.ParseMediaType(ct)
	mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var sb strings.Builder
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}
		data, _ := io.ReadAll(part)
		if part.FileName() != "" {
			fmt.Fprintf(&sb, " [file %q filename=%q ct=%q len=%d head=%q]", part.FormName(), part.FileName(), part.Header.Get("Content-Type"), len(data), data[:min(len(data), 8)])
		} else {
			fmt.Fprintf(&sb, " [%s=%q]", part.FormName(), data)
		}
	}
	return sb.String()
}

func rewriteCases() []rewriteCase {
	common := func(_ *testing.T) []byte {
		return rawMultipart(
			fieldPart("model", "m"), fieldPart("context", "{}"), fieldPart("response_format", "json"),
			fieldPart("custom_words", `["old"]`), fieldPart("language", "en"), fieldPart("prompt", "old prompt"),
			filePart("file", "a.wav", "audio/wav", "RIFFdata"), fieldPart("extra", "x"),
		)
	}
	return []rewriteCase{
		{name: "plain_passthrough", body: common},
		{name: "custom_words_replaced", body: common, opts: MultipartRewriteOpts{CustomWordsJSON: `["new"]`}},
		{name: "strip_language", body: common, opts: MultipartRewriteOpts{StripLanguage: true}},
		{name: "language_override", body: common, opts: MultipartRewriteOpts{LanguageOverride: "de"}},
		{name: "all_injections", body: common, opts: MultipartRewriteOpts{
			ForwardModel: "fm", WhisperPrompt: "wp", CustomWordsJSON: `["c"]`, LanguageOverride: "fr", InjectVerboseJSON: true,
		}},
		{name: "language_absent_override_added", body: func(*testing.T) []byte { return rawMultipart(fieldPart("x", "y")) },
			opts: MultipartRewriteOpts{LanguageOverride: "es"}},
		{name: "empty_form", body: func(*testing.T) []byte { return rawMultipart() }},
		{name: "no_boundary_found", body: func(*testing.T) []byte { return []byte("nothing here") }},
		{name: "truncated_in_headers", body: func(*testing.T) []byte { return []byte("--BND\r\nContent-Disposition: form-d") }},
		{name: "truncated_field_value", body: func(*testing.T) []byte {
			return []byte("--BND\r\nContent-Disposition: form-data; name=\"x\"\r\n\r\nabc")
		}},
		{name: "truncated_file_data", body: func(*testing.T) []byte {
			return []byte("--BND\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.wav\"\r\n\r\nRIFFabc")
		}},
		{name: "two_files_last_duration_wins", body: func(*testing.T) []byte {
			return rawMultipart(filePart("file", "a.wav", "audio/wav", "RIFF"), filePart("file", "b.wav", "audio/wav", "RIFF"))
		}},
		{name: "file_without_filename_is_field", body: func(*testing.T) []byte { return rawMultipart(fieldPart("file", "inline")) }},
		{name: "wav_file_with_duration", body: func(*testing.T) []byte {
			return rawMultipart(filePart("file", "a.wav", "audio/wav", string(goldWAV(3))))
		}},
		{name: "convert_failure_surfaces", body: func(*testing.T) []byte {
			return rawMultipart(fieldPart("x", "y"), filePart("file", "a.m4a", "audio/mp4", "notwav"))
		}, opts: MultipartRewriteOpts{ConvertToWAV: true, Ctx: context.Background()}},
		{name: "filename_with_quote_escaped", body: func(*testing.T) []byte {
			return rawMultipart(filePart("file", `we"ird.m4a`, "audio/mp4", "data"))
		}},
	}
}

func renderRewriteGolden(t *testing.T) string {
	var sb strings.Builder
	for _, c := range rewriteCases() {
		if c.name == "convert_failure_surfaces" {
			installFakeFfmpeg(t, "exit 1\n")
			resetDecodeLimits()
		}
		out, ct, dur, err := rewriteMultipart(c.body(t), "BND", c.opts)
		fmt.Fprintf(&sb, "=== %s\n", c.name)
		if err != nil {
			fmt.Fprintf(&sb, "error: %v (out nil=%v ct=%q dur=%d)\n", err, out == nil, ct, dur)
			continue
		}
		media, _, _ := mime.ParseMediaType(ct)
		fmt.Fprintf(&sb, "ct=%s dur=%d fields:%s\n", media, dur, describeRewritten(out, ct))
	}
	return sb.String()
}

func TestRewriteMultipart_GoldenBehaviour(t *testing.T) {
	got := renderRewriteGolden(t)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(rewriteGoldenPath, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(rewriteGoldenPath)
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
