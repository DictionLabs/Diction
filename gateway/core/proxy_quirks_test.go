package core

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"testing"
)

func quirkScenario(t *testing.T, name string) string {
	t.Helper()
	for _, sc := range goldScenarios(t) {
		if sc.name == name {
			return runGoldScenario(t, sc, currentTranscriptionHandler)
		}
	}
	t.Fatalf("scenario %s missing", name)
	return ""
}

func TestTranscriptionHandler_ContentLengthMatchesBodyAfterWhisperLanguageRewrite(t *testing.T) {
	out := quirkScenario(t, "quirk_stale_content_length_autodetect_whisper_language")
	if !strings.Contains(out, "header Content-Length: 21\n") || !strings.Contains(out, `body "{\"text\":\"hallo welt\"}"`) {
		t.Fatalf("declared length must match the rewritten body (21 bytes):\n%s", out)
	}
}

func TestTranscriptionHandler_RouteModelHeaderNamesRetryModelOnly(t *testing.T) {
	out := quirkScenario(t, "quirk_duplicate_route_model_header_on_retry")
	if !strings.Contains(out, "header X-Diction-Route-Model: fallback\n") || strings.Contains(out, "primary|fallback") {
		t.Fatalf("Route-Model must name only the model that produced the result:\n%s", out)
	}
}

func rfc5987(s string) string {
	var sb strings.Builder
	for _, b := range []byte(s) {
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '.' {
			sb.WriteByte(b)
		} else {
			fmt.Fprintf(&sb, "%%%02X", b)
		}
	}
	return sb.String()
}

func rewrittenFileHeader(t *testing.T, filename string) (disposition, rawBody string) {
	t.Helper()
	in := "--BND\r\nContent-Disposition: form-data; name=\"file\"; filename*=UTF-8''" + rfc5987(filename) +
		"\r\nContent-Type: audio/mp4\r\n\r\ndata\r\n--BND--\r\n"
	out, ct, _, err := rewriteMultipart([]byte(in), "BND", MultipartRewriteOpts{})
	if err != nil {
		t.Fatal(err)
	}
	r := multipart.NewReader(bytes.NewReader(out), parseBoundary(ct))
	p, err := r.NextPart()
	if err != nil {
		t.Fatalf("output is not valid multipart: %v\n%q", err, out)
	}
	if _, err := r.NextPart(); err != io.EOF {
		t.Fatalf("filename injected an extra part or header: %v\n%q", err, out)
	}
	if len(p.Header) != 1 {
		t.Fatalf("injected headers: %v", p.Header)
	}
	return p.Header.Get("Content-Disposition"), string(out)
}

func TestRewriteMultipart_EscapesFilename(t *testing.T) {
	cases := []struct{ name, filename, wantParsed string }{
		{"quote", `we"ird.m4a`, `we"ird.m4a`},
		{"backslash", `a\b.m4a`, `a\b.m4a`},
		{"unicode", "h\u00e9llo \u4e2d.m4a", "h\u00e9llo \u4e2d.m4a"},
		{"cr", "a\rb.m4a", "a%0Db.m4a"},
		{"lf", "a\nb.m4a", "a%0Ab.m4a"},
		{"crlf_injection", "x.m4a\r\nX-Evil: 1", "x.m4a%0D%0AX-Evil: 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cd, _ := rewrittenFileHeader(t, c.filename)
			_, params, err := mime.ParseMediaType(cd)
			if err != nil {
				t.Fatalf("unparseable disposition %q: %v", cd, err)
			}
			if params["filename"] != c.wantParsed || params["name"] != "file" {
				t.Fatalf("filename round trip: got %q want %q (header %q)", params["filename"], c.wantParsed, cd)
			}
		})
	}
}

func TestTranscriptionHandler_RouteModelHeaderSingleValueOnRetry(t *testing.T) {
	out := quirkScenario(t, "quirk_duplicate_route_model_header_on_retry")
	if n := strings.Count(out, "header X-Diction-Route-Model:"); n != 1 {
		t.Fatalf("want exactly one Route-Model header line, got %d:\n%s", n, out)
	}
	if !strings.Contains(out, "header X-Diction-Route-Retry: true\n") {
		t.Fatalf("retry marker lost:\n%s", out)
	}
}
