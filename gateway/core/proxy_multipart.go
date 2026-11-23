package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"strings"
)

func parseBoundary(contentType string) string {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		return ""
	}
	return params["boundary"]
}

type MultipartRewriteOpts struct {
	ConvertToWAV  bool
	ForwardModel  string
	WhisperPrompt string
	StripLanguage bool

	LanguageOverride string

	InjectVerboseJSON bool

	CustomWordsJSON string
	Ctx             context.Context
}

type multipartRewriter struct {
	opts            MultipartRewriteOpts
	writer          *multipart.Writer
	audioDurationMs int64
}

func rewriteMultipart(body []byte, boundary string, opts MultipartRewriteOpts) ([]byte, string, int64, error) {
	reader := multipart.NewReader(bytes.NewReader(body), boundary)

	var buf bytes.Buffer
	rw := &multipartRewriter{opts: opts, writer: multipart.NewWriter(&buf)}

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", 0, fmt.Errorf("read multipart: %w", err)
		}
		if err := rw.copyPart(part); err != nil {
			return nil, "", 0, err
		}
	}

	if err := rw.appendFields(); err != nil {
		return nil, "", 0, err
	}

	if err := rw.writer.Close(); err != nil {
		return nil, "", 0, fmt.Errorf("close multipart writer: %w", err)
	}

	return buf.Bytes(), rw.writer.FormDataContentType(), rw.audioDurationMs, nil
}

func (rw *multipartRewriter) dropsPart(name string) bool {
	switch name {
	case fieldModel, "context", fieldResponseFormat:
		return true
	case customWordsField:
		return rw.opts.CustomWordsJSON != ""
	case "language":
		return rw.opts.StripLanguage || rw.opts.LanguageOverride != ""
	}
	return false
}

func (rw *multipartRewriter) copyPart(part *multipart.Part) error {
	name := part.FormName()
	if rw.dropsPart(name) {
		_ = part.Close()
		return nil
	}
	if name == "file" {
		return rw.copyFile(part)
	}
	return rw.copyField(part, name)
}

// filenameEscaper quotes like mime/multipart and encodes CR/LF against header injection.
var filenameEscaper = strings.NewReplacer("\\", "\\\\", `"`, "\\\"", "\r", "%0D", "\n", "%0A")

func escapeFilename(name string) string {
	return filenameEscaper.Replace(name)
}

func (rw *multipartRewriter) copyFile(part *multipart.Part) error {
	audioData, err := io.ReadAll(part)
	filename := part.FileName()
	_ = part.Close()
	if err != nil {
		return fmt.Errorf("read audio: %w", err)
	}

	if rw.opts.ConvertToWAV {
		audioData, err = convertToWAVBytesCtx(rw.opts.Ctx, audioData, filename)
		if err != nil {
			return err
		}
		filename = fileNameWav
	}

	rw.audioDurationMs = ParseWAVDurationMs(audioData)

	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, escapeFilename(filename)))
	if rw.opts.ConvertToWAV {
		partHeader.Set("Content-Type", "audio/wav")
	}
	dst, err := rw.writer.CreatePart(partHeader)
	if err != nil {
		return fmt.Errorf("create file part: %w", err)
	}
	if _, err := dst.Write(audioData); err != nil {
		return fmt.Errorf("write audio: %w", err)
	}
	return nil
}

func (rw *multipartRewriter) copyField(part *multipart.Part, name string) error {
	data, err := io.ReadAll(part)
	_ = part.Close()
	if err != nil {
		return fmt.Errorf("read part %s: %w", name, err)
	}
	if err := rw.writer.WriteField(name, string(data)); err != nil {
		return fmt.Errorf("write field %s: %w", name, err)
	}
	return nil
}

func (rw *multipartRewriter) appendFields() error {
	fields := []struct {
		name, value, errFormat string
		on                     bool
	}{
		{fieldModel, rw.opts.ForwardModel, "write model field: %w", rw.opts.ForwardModel != ""},
		{"prompt", rw.opts.WhisperPrompt, "write prompt field: %w", rw.opts.WhisperPrompt != ""},
		{customWordsField, rw.opts.CustomWordsJSON, "write custom_words field: %w", rw.opts.CustomWordsJSON != ""},
		{"language", rw.opts.LanguageOverride, "write language override field: %w", rw.opts.LanguageOverride != ""},
		{fieldResponseFormat, "verbose_json", "write response_format field: %w", rw.opts.InjectVerboseJSON},
	}
	for _, f := range fields {
		if !f.on {
			continue
		}
		if err := rw.writer.WriteField(f.name, f.value); err != nil {
			return fmt.Errorf(f.errFormat, err)
		}
	}
	return nil
}

func extractFormField(body []byte, boundary, fieldName string) string {
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		if part.FormName() == fieldName {
			data, _ := io.ReadAll(part)
			_ = part.Close()
			return string(data)
		}
		_ = part.Close()
	}
	return ""
}
