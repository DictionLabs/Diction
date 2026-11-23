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
