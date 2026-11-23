package core

import (
	"net/http"
	"os"
	"strconv"
	"sync"
)

var StreamPrincipal func(r *http.Request) string

const defaultOpusMaxPerPrincipal = 3

func opusMaxPerPrincipal() int {
	if s := os.Getenv("DICTION_OPUS_MAX_PER_PRINCIPAL"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v >= 0 {
			return v
		}
	}
	return defaultOpusMaxPerPrincipal
}

type PrincipalLimiter struct {
	mu sync.Mutex
	n  map[string]int
}

func (p *PrincipalLimiter) TryAcquire(key string, limit int) (release func(), ok bool) {
	if key == "" || limit <= 0 {
		return func() {}, true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.n == nil {
		p.n = make(map[string]int)
	}
	if p.n[key] >= limit {
		return nil, false
	}
	p.n[key]++
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			if p.n[key] <= 1 {
				delete(p.n, key)
			} else {
				p.n[key]--
			}
		})
	}, true
}

func (p *PrincipalLimiter) Count(key string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.n[key]
}

var opusPrincipals = &PrincipalLimiter{}
