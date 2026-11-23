package core

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestHealthState_SetAndGet(t *testing.T) {
	h := newHealthState()

	h.set("small", true)
	if !h.get("small") {
		t.Error("want true after set(true)")
	}

	h.set("small", false)
	if h.get("small") {
		t.Error("want false after set(false)")
	}
}

func TestHealthState_UnknownBackend(t *testing.T) {
	h := newHealthState()
	if h.get("nonexistent") {
		t.Error("want false for unknown backend")
	}
}

func TestHealthState_ConcurrentAccess(_ *testing.T) {
	h := newHealthState()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(v bool) {
			defer wg.Done()
			h.set("small", v)
		}(i%2 == 0)
		go func() {
			defer wg.Done()
			h.get("small")
		}()
	}
	wg.Wait()
}

func TestGateway_SetAndIsServiceHealthy(t *testing.T) {
	g := &Gateway{health: newHealthState()}
	g.SetServiceHealth("wlk", true)
	if !g.IsServiceHealthy("wlk") {
		t.Error("want true after SetServiceHealth(true)")
	}
	g.SetServiceHealth("wlk", false)
	if g.IsServiceHealthy("wlk") {
		t.Error("want false after SetServiceHealth(false)")
	}
}

func TestStartHealthChecker_UpdatesState(t *testing.T) {
	healthyBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer healthyBackend.Close()

	g := &Gateway{
		backends: []Backend{
			{Name: "testmodel", URL: healthyBackend.URL, Aliases: []string{"testmodel"}},
		},
		health:       newHealthState(),
		defaultModel: "testmodel",
		maxBodySize:  10 << 20,
	}

	g.startHealthChecker()

	if !g.health.get("testmodel") {
		t.Error("expected testmodel to be healthy after health check")
	}
}

func TestStartHealthChecker_UnhealthyBackend(t *testing.T) {
	unhealthyBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer unhealthyBackend.Close()

	g := &Gateway{
		backends: []Backend{
			{Name: "badmodel", URL: unhealthyBackend.URL, Aliases: []string{"badmodel"}},
		},
		health:       newHealthState(),
		defaultModel: "badmodel",
		maxBodySize:  10 << 20,
	}

	g.startHealthChecker()

	if g.health.get("badmodel") {
		t.Error("expected badmodel to be unhealthy")
	}
}
