package core

import (
	"bytes"
	"io"
	"mime/multipart"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func buildMultipart(t *testing.T, fields map[string]string, fileName, fileContent string) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatalf("WriteField %s: %v", k, err)
		}
	}
	if fileName != "" {
		fw, err := w.CreateFormFile("file", fileName)
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		fw.Write([]byte(fileContent))
	}
	w.Close()
	return buf.Bytes(), w.FormDataContentType()
}

func TestParseBoundary_ExtractsBoundary(t *testing.T) {
	_, ct := buildMultipart(t, map[string]string{}, "audio.m4a", "fake-audio")
	boundary := parseBoundary(ct)
	if boundary == "" {
		t.Error("expected non-empty boundary")
	}
}

func TestParseBoundary_InvalidContentType(t *testing.T) {
	boundary := parseBoundary("application/json")
	if boundary != "" {
		t.Errorf("boundary: want empty for non-multipart, got %s", boundary)
	}
}

func TestParseBoundary_ExplicitBoundary(t *testing.T) {
	boundary := parseBoundary("multipart/form-data; boundary=abc123")
	if boundary != "abc123" {
		t.Errorf("boundary: want abc123, got %s", boundary)
	}
}

func TestRewriteMultipart_StripsModelField(t *testing.T) {
	body, ct := buildMultipart(t, map[string]string{"model": "medium", "language": "en"}, "audio.m4a", "fake-audio-data")
	boundary := parseBoundary(ct)

	rewritten, newCT, _, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{})
	if err != nil {
		t.Fatalf("rewriteMultipart error: %v", err)
	}

	_, params, _ := parseMediaType(newCT)
	newBoundary := params["boundary"]
	reader := multipart.NewReader(bytes.NewReader(rewritten), newBoundary)
	foundModel := false
	foundLanguage := false
	foundFile := false
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read rewritten part: %v", err)
		}
		switch part.FormName() {
		case "model":
			foundModel = true
		case "language":
			foundLanguage = true
		case "file":
			foundFile = true
		}
		part.Close()
	}

	if foundModel {
		t.Error("model field should have been stripped")
	}
	if !foundLanguage {
		t.Error("language field should be preserved")
	}
	if !foundFile {
		t.Error("file part should be preserved")
	}
}

func TestRewriteMultipart_PreservesFileContent(t *testing.T) {
	const audioContent = "this-is-fake-audio-bytes"
	body, ct := buildMultipart(t, map[string]string{"model": "small"}, "audio.m4a", audioContent)
	boundary := parseBoundary(ct)

	rewritten, newCT, _, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{})
	if err != nil {
		t.Fatalf("rewriteMultipart error: %v", err)
	}

	_, params, _ := parseMediaType(newCT)
	reader := multipart.NewReader(bytes.NewReader(rewritten), params["boundary"])
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read rewritten part: %v", err)
		}
		if part.FormName() == "file" {
			data, _ := io.ReadAll(part)
			if string(data) != audioContent {
				t.Errorf("file content: want %q, got %q", audioContent, string(data))
			}
		}
		part.Close()
	}
}

func TestRewriteMultipart_NoModelField(t *testing.T) {
	body, ct := buildMultipart(t, map[string]string{}, "audio.m4a", "audio")
	boundary := parseBoundary(ct)

	_, _, _, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func presentFields(t *testing.T, body []byte, ct string) map[string]bool {
	t.Helper()
	_, params, _ := parseMediaType(ct)
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	found := map[string]bool{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		found[part.FormName()] = true
		part.Close()
	}
	return found
}

func TestRewriteMultipart_StripLanguageWhenAutoDetect(t *testing.T) {
	body, ct := buildMultipart(t, map[string]string{"language": "auto"}, "audio.m4a", "audio")
	boundary := parseBoundary(ct)

	rewritten, newCT, _, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{
		StripLanguage: true,
	})
	if err != nil {
		t.Fatalf("rewriteMultipart error: %v", err)
	}

	found := presentFields(t, rewritten, newCT)
	if found["language"] {
		t.Error("language must be stripped when StripLanguage=true")
	}
}

func TestRewriteMultipart_KeepsLanguageWhenSingleLang(t *testing.T) {
	body, ct := buildMultipart(t, map[string]string{"language": "fr"}, "audio.m4a", "audio")
	boundary := parseBoundary(ct)

	rewritten, newCT, _, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{})
	if err != nil {
		t.Fatalf("rewriteMultipart error: %v", err)
	}

	found := presentFields(t, rewritten, newCT)
	if !found["language"] {
		t.Error("language must be preserved when StripLanguage=false")
	}
}

func TestRewriteMultipart_ConvertToWAV(t *testing.T) {
	wavData := []byte("RIFF\x00\x00\x00\x00WAVEfmt ")
	body, ct := buildMultipart(t, map[string]string{"language": "en"}, "audio.m4a", string(wavData))
	boundary := parseBoundary(ct)

	rewritten, newCT, _, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{ConvertToWAV: true})
	if err != nil {
		t.Fatalf("rewriteMultipart error: %v", err)
	}

	_, params, _ := parseMediaType(newCT)
	reader := multipart.NewReader(bytes.NewReader(rewritten), params["boundary"])
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		if part.FormName() == "file" {
			if part.Header.Get("Content-Type") != "audio/wav" {
				t.Errorf("Content-Type: want audio/wav, got %s", part.Header.Get("Content-Type"))
			}
			if part.FileName() != "audio.wav" {
				t.Errorf("filename: want audio.wav, got %s", part.FileName())
			}
		}
		part.Close()
	}
}

func TestConvertToWAVBytes_PassthroughWhenAlreadyWAV(t *testing.T) {
	wavData := []byte("RIFF\x00\x00\x00\x00WAVEfmt ")
	result, err := convertToWAVBytes(wavData, "audio.wav")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(result, wavData) {
		t.Error("expected WAV data to be passed through unchanged")
	}
}

func TestConvertToWAVBytes_FFmpegErrorOnGarbage(t *testing.T) {
	garbage := bytes.Repeat([]byte{0xDE, 0xAD, 0xBE, 0xEF}, 64)
	_, err := convertToWAVBytes(garbage, "audio.m4a")
	if err == nil {
		t.Fatal("expected error when ffmpeg cannot decode garbage audio")
	}
}

func TestConvertToWAVBytes_EmptyFilename(t *testing.T) {
	garbage := bytes.Repeat([]byte{0xFF, 0xFE}, 32)
	_, err := convertToWAVBytes(garbage, "")
	if err == nil {
		t.Fatal("expected error for undecipherable audio without filename")
	}
}

func TestRewriteMultipart_InjectsWhisperPrompt(t *testing.T) {
	body, ct := buildMultipart(t, map[string]string{"language": "en"}, "audio.m4a", "audio")
	boundary := parseBoundary(ct)

	rewritten, newCT, _, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{WhisperPrompt: "Kubernetes, PostgreSQL"})
	if err != nil {
		t.Fatalf("rewriteMultipart: %v", err)
	}

	_, params, _ := parseMediaType(newCT)
	reader := multipart.NewReader(bytes.NewReader(rewritten), params["boundary"])
	foundPrompt := false
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		if part.FormName() == "prompt" {
			data, _ := io.ReadAll(part)
			foundPrompt = true
			if string(data) != "Kubernetes, PostgreSQL" {
				t.Errorf("prompt: want %q, got %q", "Kubernetes, PostgreSQL", string(data))
			}
		}
		part.Close()
	}
	if !foundPrompt {
		t.Error("expected prompt field in rewritten multipart")
	}
}

func TestRewriteMultipart_NoPromptWhenEmpty(t *testing.T) {
	body, ct := buildMultipart(t, map[string]string{"language": "en"}, "audio.m4a", "audio")
	boundary := parseBoundary(ct)

	rewritten, newCT, _, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{})
	if err != nil {
		t.Fatalf("rewriteMultipart: %v", err)
	}

	_, params, _ := parseMediaType(newCT)
	reader := multipart.NewReader(bytes.NewReader(rewritten), params["boundary"])
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		if part.FormName() == "prompt" {
			t.Error("prompt field should not be present when whisperPrompt is empty")
		}
		part.Close()
	}
}

func TestRewriteMultipart_StripsContextField(t *testing.T) {
	body, ct := buildMultipart(t, map[string]string{"context": `{"before":"x"}`, "language": "en"}, "audio.wav", "audio")
	boundary := parseBoundary(ct)
	rewritten, newCT, _, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{})
	if err != nil {
		t.Fatalf("rewriteMultipart: %v", err)
	}
	_, params, _ := parseMediaType(newCT)
	reader := multipart.NewReader(bytes.NewReader(rewritten), params["boundary"])
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		if part.FormName() == "context" {
			t.Error("context field should have been stripped")
		}
		part.Close()
	}
}

func TestConvertToWAVBytes_SuccessfulConversion(t *testing.T) {
	tmpDir := t.TempDir()
	mp3Path := filepath.Join(tmpDir, "silence.mp3")
	gen := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "anullsrc=r=16000:cl=mono",
		"-t", "0.01", "-f", "mp3", mp3Path)
	gen.Stderr = io.Discard
	if err := gen.Run(); err != nil {
		t.Skipf("ffmpeg not available for conversion test: %v", err)
	}
	mp3Data, err := os.ReadFile(mp3Path)
	if err != nil {
		t.Fatal(err)
	}

	result, err := convertToWAVBytes(mp3Data, "silence.mp3")
	if err != nil {
		t.Fatalf("convertToWAVBytes: %v", err)
	}
	if len(result) < 4 || string(result[:4]) != "RIFF" {
		t.Error("expected WAV output (RIFF header)")
	}
}

func TestRewriteMultipart_StripsResponseFormat(t *testing.T) {
	body, ct := buildMultipart(t, map[string]string{"response_format": "text", "language": "en"}, "audio.m4a", "audio")
	boundary := parseBoundary(ct)

	rewritten, newCT, _, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{})
	if err != nil {
		t.Fatalf("rewriteMultipart: %v", err)
	}

	_, params, _ := parseMediaType(newCT)
	reader := multipart.NewReader(bytes.NewReader(rewritten), params["boundary"])
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		if part.FormName() == "response_format" {
			t.Error("response_format should have been stripped from multipart")
		}
		part.Close()
	}
}

func fieldValues(t *testing.T, body []byte, ct string) map[string]string {
	t.Helper()
	_, params, _ := parseMediaType(ct)
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	vals := map[string]string{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("fieldValues: read part: %v", err)
		}
		if part.FileName() == "" {
			data, _ := io.ReadAll(part)
			vals[part.FormName()] = string(data)
		}
		part.Close()
	}
	return vals
}

func TestRewriteMultipart_LanguageOverride(t *testing.T) {
	body, ct := buildMultipart(t, map[string]string{"language": "auto"}, "audio.m4a", "audio")
	boundary := parseBoundary(ct)

	rewritten, newCT, _, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{
		LanguageOverride: "cs",
	})
	if err != nil {
		t.Fatalf("rewriteMultipart: %v", err)
	}

	vals := fieldValues(t, rewritten, newCT)
	if vals["language"] != "cs" {
		t.Errorf("language: want cs, got %q", vals["language"])
	}
}

func TestRewriteMultipart_InjectVerboseJSON(t *testing.T) {
	body, ct := buildMultipart(t, map[string]string{"language": "en"}, "audio.m4a", "audio")
	boundary := parseBoundary(ct)

	rewritten, newCT, _, err := rewriteMultipart(body, boundary, MultipartRewriteOpts{
		InjectVerboseJSON: true,
	})
	if err != nil {
		t.Fatalf("rewriteMultipart: %v", err)
	}

	vals := fieldValues(t, rewritten, newCT)
	if vals["response_format"] != "verbose_json" {
		t.Errorf("response_format: want verbose_json, got %q", vals["response_format"])
	}
}
