package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"time"
)

func (g *Gateway) proxyToBackend(
	ctx context.Context, target *url.URL, p audioPayload, backend *Backend,
	language, whisperPrompt, customWordsJSON string,
) (string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	filePart, err := writer.CreateFormFile("file", p.filename)
	if err != nil {
		return "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(filePart, bytes.NewReader(p.data)); err != nil {
		return "", fmt.Errorf("copy audio: %w", err)
	}

	if backend.ForwardModel != "" {
		_ = writer.WriteField("model", backend.ForwardModel)
	}
	if language != "" {
		_ = writer.WriteField("language", language)
	}
	if whisperPrompt != "" {
		_ = writer.WriteField("prompt", whisperPrompt)
	}
	if cw := customWordsFor(backend, customWordsJSON); cw != "" {
		_ = writer.WriteField(customWordsField, cw)
	}
	_ = writer.Close()

	transcriptionPath := routeTranscriptions
	if backend.TargetPath != "" {
		transcriptionPath = backend.TargetPath
	}
	backendURL := target.String() + transcriptionPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, backendURL, &body)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if backend.AuthHeader != "" {
		req.Header.Set("Authorization", backend.AuthHeader)
	}

	client := &http.Client{Timeout: 90 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("backend request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("backend returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if whisperPrompt != "" {
		return stripWhisperPromptEcho(result.Text), nil
	}
	return result.Text, nil
}
