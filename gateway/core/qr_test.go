package core

import (
	"net/url"
	"strings"
	"testing"
)

func TestPairingLink(t *testing.T) {
	link := PairingLink("https://gw.example.com:8443", "dk_abc123")
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if u.Scheme != "diction" || u.Host != "pair" {
		t.Fatalf("link = %q, want diction://pair", link)
	}
	q := u.Query()
	if q.Get("url") != "https://gw.example.com:8443" {
		t.Fatalf("url param = %q", q.Get("url"))
	}
	if q.Get("key") != "dk_abc123" {
		t.Fatalf("key param = %q", q.Get("key"))
	}
}

func TestPairingLinkKeyOnly(t *testing.T) {
	link := PairingLink("", "dk_abc123")
	u, _ := url.Parse(link)
	if u.Query().Has("url") {
		t.Fatalf("key-only link must omit url param: %q", link)
	}
	if u.Query().Get("key") != "dk_abc123" {
		t.Fatalf("key param = %q", u.Query().Get("key"))
	}
}

func TestPairingLinkEscapesURL(t *testing.T) {
	link := PairingLink("http://192.168.0.220:8080", "dk_k")
	// The embedded URL's :// must be percent-encoded inside the query.
	if strings.Count(link, "://") != 1 {
		t.Fatalf("embedded URL not escaped: %q", link)
	}
	u, _ := url.Parse(link)
	if u.Query().Get("url") != "http://192.168.0.220:8080" {
		t.Fatalf("round-trip failed: %q", u.Query().Get("url"))
	}
}

func TestQRTerminalArt(t *testing.T) {
	art, err := qrTerminalArt("diction://pair?key=dk_test")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	lines := strings.Split(strings.TrimRight(art, "\n"), "\n")
	if len(lines) < 10 {
		t.Fatalf("suspiciously small QR: %d lines", len(lines))
	}
	// All lines equal width, and narrow enough for an 80-column terminal.
	width := len([]rune(lines[0]))
	if width > 80 {
		t.Fatalf("QR too wide for a terminal: %d cols", width)
	}
	for i, line := range lines {
		if len([]rune(line)) != width {
			t.Fatalf("line %d width %d != %d", i, len([]rune(line)), width)
		}
		for _, r := range line {
			if r != ' ' && r != '█' && r != '▀' && r != '▄' {
				t.Fatalf("unexpected rune %q in QR art", r)
			}
		}
	}
}
