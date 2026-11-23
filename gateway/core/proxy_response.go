package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
)

func setResponseBody(resp *http.Response, body []byte) {
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
}

func addOptionalFields(result map[string]any, mode, status string, autoEdit bool) {
	if mode != "" {
		result["mode"] = mode
	}
	if status != "" {
		result["status"] = status
	}
	if autoEdit {
		result["auto_edit"] = true
	}
}

func writeEncryptedResponse(resp *http.Response, transcript, mode, status, e2eClientKey string, autoEdit bool) bool {
	ct, pk, err := EncryptTranscript(transcript, e2eClientKey)
	if err == nil {
		result := map[string]any{
			"e2e": map[string]string{"ct": ct, "pk": pk},
		}
		addOptionalFields(result, mode, status, autoEdit)
		newBody, _ := json.Marshal(result)
		setResponseBody(resp, newBody)
		return true
	}
	log.Printf("e2e encrypt error: %v", err)
	reportError(resp.Request.Context(), ErrorEvent{
		Source:     "e2e",
		Kind:       "e2e_encrypt",
		Endpoint:   routeTranscriptions,
		HTTPStatus: resp.StatusCode,
		Hint:       "transcript e2e encrypt failed; falling back to plain",
	})
	return false
}

func writeTranscriptionResponse(resp *http.Response, transcript, mode, status, responseFormat, e2eClientKey string, autoEdit bool) {
	if responseFormat == formatText {
		resp.Header.Set("Content-Type", "text/plain; charset=utf-8")
		setResponseBody(resp, []byte(transcript))
		return
	}

	if e2eClientKey != "" && writeEncryptedResponse(resp, transcript, mode, status, e2eClientKey, autoEdit) {
		return
	}

	result := map[string]any{"text": transcript}
	addOptionalFields(result, mode, status, autoEdit)
	newBody, _ := json.Marshal(result)
	setResponseBody(resp, newBody)
}

func flushRecorder(rec *httptest.ResponseRecorder, w http.ResponseWriter) {
	for k, vals := range rec.Header() {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())
}
