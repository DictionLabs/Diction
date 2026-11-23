package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DictionLabs/Diction/gateway/core/transcript"
	"github.com/coder/websocket"
)

type streamReadPhase uint8

const (
	readAwaitingContext streamReadPhase = iota
	readContextSeen
	readAudio
)

type streamOutcome struct {
	set         bool
	ctx         context.Context
	event       *ErrorEvent
	failType    string
	httpStatus  int
	httpBody    string
	closeCode   websocket.StatusCode
	closeReason string
}

func (o *streamOutcome) end(ctx context.Context, ev *ErrorEvent, failType string, code websocket.StatusCode, reason string) {
	if o.set {
		return
	}
	*o = streamOutcome{set: true, ctx: ctx, event: ev, failType: failType, closeCode: code, closeReason: reason}
}

func (o *streamOutcome) endHTTP(ctx context.Context, ev *ErrorEvent, failType string, status int, body string) {
	if o.set {
		return
	}
	*o = streamOutcome{set: true, ctx: ctx, event: ev, failType: failType, httpStatus: status, httpBody: body}
}

func (o *streamOutcome) emit(w http.ResponseWriter, conn *websocket.Conn) {
	if !o.set {
		return
	}
	if o.event != nil && OnError != nil {
		OnError(o.ctx, *o.event)
	}
	if o.failType != "" && OnRequestFailed != nil {
		OnRequestFailed(o.ctx, o.failType)
	}
	switch {
	case o.httpStatus != 0:
		http.Error(w, o.httpBody, o.httpStatus)
	case o.closeCode != 0 && conn != nil:
		CloseWSWithTimeout(conn, o.closeCode, o.closeReason, 2*time.Second)
	}
}

const (
	kindSTTBackendError     = "stt_backend_error"
	kindSTTBackend5xx       = "stt_backend_5xx"
	kindSTTUpstreamCanceled = "stt_upstream_canceled"
	kindSTTHallucination    = "stt_hallucination"
)

func classifyBackendFailure(err error) (kind, hint string, status int) {
	switch {
	case errors.Is(err, transcript.ErrSTTHallucination):
		return kindSTTHallucination, "backend returned repeated-token hallucination", 0
	case errors.Is(err, context.Canceled):
		return kindSTTUpstreamCanceled, "upstream canceled (client disconnect or new request)", 0
	case errors.Is(err, context.DeadlineExceeded):
		return kindSTTBackendError, "backend timed out", 0
	}
	if code := backendStatusFromErr(err.Error()); code > 0 {
		if code >= 500 {
			return kindSTTBackend5xx, fmt.Sprintf("backend returned %d", code), code
		}
		return kindSTTBackendError, fmt.Sprintf("backend returned %d", code), code
	}
	if strings.HasPrefix(err.Error(), "decode response:") {
		return kindSTTBackendError, "backend response decode failed", 0
	}
	return kindSTTBackendError, "backend transport failed", 0
}

func backendStatusFromErr(s string) int {
	const marker = "backend returned "
	i := strings.Index(s, marker)
	if i < 0 {
		return 0
	}
	rest := s[i+len(marker):]
	n := 0
	for n < len(rest) && rest[n] >= '0' && rest[n] <= '9' {
		n++
	}
	if n != 3 {
		return 0
	}
	code, err := strconv.Atoi(rest[:3])
	if err != nil {
		return 0
	}
	return code
}
