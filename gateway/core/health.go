package core

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type healthEntry struct {
	probeOK      bool
	probed       bool
	demotedUntil time.Time
	untilProbe   int
	probeEpoch   uint64
	reason       string
}

func (e healthEntry) healthy(now time.Time) bool {
	return e.probed && e.probeOK && e.untilProbe == 0 && !now.Before(e.demotedUntil)
}

type healthEventKind int

const (
	healthProbe healthEventKind = iota
	healthDemote
	healthUndo
)

type healthEvent struct {
	kind   healthEventKind
	ok     bool
	reason string
	ttl    time.Duration
	token  demotionToken
}

type demotionToken struct {
	until      time.Time
	untilProbe bool
	epoch      uint64
}

type healthState struct {
	mu      sync.RWMutex
	entries map[string]healthEntry
	now     func() time.Time
}

func newHealthState() *healthState {
	return &healthState{
		entries: make(map[string]healthEntry),
		now:     time.Now,
	}
}

func (h *healthState) observe(name string, ev healthEvent) demotionToken {
	h.mu.Lock()
	defer h.mu.Unlock()
	e := h.entries[name]
	var tok demotionToken
	switch ev.kind {
	case healthProbe:
		e.probeOK = ev.ok
		e.probed = true
		e.untilProbe = 0
		e.probeEpoch++
	case healthDemote:
		e.reason = ev.reason
		if ev.ttl > 0 {
			if until := h.now().Add(ev.ttl); until.After(e.demotedUntil) {
				e.demotedUntil = until
			}
			e.untilProbe = 0
			tok = demotionToken{until: e.demotedUntil}
		} else {
			e.untilProbe++
			tok = demotionToken{untilProbe: true, epoch: e.probeEpoch}
		}
	case healthUndo:
		switch {
		case ev.token.untilProbe:
			if ev.token.epoch == e.probeEpoch && e.untilProbe > 0 {
				e.untilProbe--
			}
		case !ev.token.until.IsZero() && e.demotedUntil.Equal(ev.token.until):
			e.demotedUntil = time.Time{}
		}
	}
	h.entries[name] = e
	return tok
}

func (h *healthState) probe(name string, ok bool) {
	h.observe(name, healthEvent{kind: healthProbe, ok: ok})
}

func (h *healthState) demote(name, reason string, ttl time.Duration) demotionToken {
	return h.observe(name, healthEvent{kind: healthDemote, reason: reason, ttl: ttl})
}

func (h *healthState) undoDemotion(name string, tok demotionToken) {
	h.observe(name, healthEvent{kind: healthUndo, token: tok})
}

func (h *healthState) set(name string, ok bool) { h.probe(name, ok) }

func (h *healthState) get(name string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.entries[name].healthy(h.now())
}

func (g *Gateway) SetServiceHealth(name string, ok bool) {
	g.health.probe(name, ok)
}

func (g *Gateway) IsServiceHealthy(name string) bool {
	return g.healthy(name)
}

func (g *Gateway) healthy(name string) bool {
	for i := range g.backends {
		b := &g.backends[i]
		if !b.SkipHealthCheck {
			continue
		}
		if b.Name == name {
			return true
		}
		for _, a := range b.Aliases {
			if a == name {
				return true
			}
		}
	}
	return g.health.get(name)
}

func (g *Gateway) startHealthChecker() {
	check := func() {
		client := &http.Client{Timeout: 5 * time.Second}
		var wg sync.WaitGroup
		for _, b := range g.backends {
			if b.Disabled {
				continue
			}
			if b.SkipHealthCheck {
				continue
			}
			wg.Add(1)
			go func(b Backend) {
				defer wg.Done()
				var resp *http.Response
				req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, b.URL+"/health", nil)
				if err == nil {
					resp, err = client.Do(req)
				}
				ok := err == nil && resp != nil && resp.StatusCode == http.StatusOK
				if resp != nil {
					_ = resp.Body.Close()
				}
				g.health.probe(b.Name, ok)
			}(b)
		}
		wg.Wait()
	}
	check()
	go func() {
		ticker := time.NewTicker(120 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			check()
		}
	}()
}

const defaultSTTDemotionTTL = 120 * time.Second

func sttDemotionTTLFromEnv() time.Duration {
	v := strings.TrimSpace(os.Getenv("DICTION_STT_DEMOTION_TTL"))
	if v == "" {
		return defaultSTTDemotionTTL
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if d, err := time.ParseDuration(v); err == nil && d >= 0 {
		return d
	}
	log.Printf("DICTION_STT_DEMOTION_TTL=%s unparsable — using %v", strconv.Quote(v), defaultSTTDemotionTTL)
	return defaultSTTDemotionTTL
}

func (g *Gateway) demoteAfterFailure(name, reason string, hasAlternative bool) {
	if hasAlternative && g.demotionTTL > 0 {
		tok := g.health.demote(name, reason, g.demotionTTL)
		log.Printf("health: demoted %s (%s) until %s", name, reason, tok.until.Format(time.RFC3339))
		return
	}
	g.health.demote(name, reason, 0)
	log.Printf("health: demoted %s (%s) until next probe", name, reason)
}
