package core

import (
	"os"
	"strings"
)

// WebSocketOriginPatterns reads DICTION_WS_ALLOWED_ORIGINS: extra browser origin host patterns.
func WebSocketOriginPatterns() []string {
	var out []string
	for _, p := range strings.Split(os.Getenv("DICTION_WS_ALLOWED_ORIGINS"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ErrTypeWSOriginRejected: upgrade refused 403 by the Origin check, not an STT fault.
const ErrTypeWSOriginRejected = "ws_origin_rejected"

// WSAcceptErrorType gives an Origin rejection its own type, other accept errors stt_error.
func WSAcceptErrorType(err error) string {
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "request Origin ") || strings.Contains(msg, "failed to parse Origin header") {
			return ErrTypeWSOriginRejected
		}
	}
	return errTypeSTTError
}
