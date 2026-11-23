package core

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func probeResult(g *Gateway, name string, ok bool) { g.health.set(name, ok) }

func statusServer(t *testing.T, status int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func transportFaultBackend(t *testing.T, onHit func()) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if onHit != nil {
			onHit()
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			conn.Close()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func wsRouteRefused(t *testing.T, srv *httptest.Server) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, wsURL(srv, "language=en"), nil)
	if err == nil {
		conn.CloseNow()
		return false
	}
	return resp != nil && resp.StatusCode == http.StatusServiceUnavailable
}

func closedURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	u := "http://" + l.Addr().String()
	l.Close()
	return u
}

func TestHealth_ProbeSetsAvailability(t *testing.T) {
	up, _ := statusServer(t, http.StatusOK)
	sick, _ := statusServer(t, http.StatusServiceUnavailable)
	disabled, disabledHits := statusServer(t, http.StatusOK)
	g := &Gateway{
		backends: []Backend{
			{Name: "up", URL: up.URL},
			{Name: "sick", URL: sick.URL},
			{Name: "gone", URL: closedURL(t)},
			{Name: "off", URL: disabled.URL, Disabled: true},
		},
		health: newHealthState(),
	}
	g.startHealthChecker()
	for name, want := range map[string]bool{"up": true, "sick": false, "gone": false, "off": false} {
		if got := g.health.get(name); got != want {
			t.Errorf("%s: healthy=%v, want %v", name, got, want)
		}
	}
	if n := disabledHits.Load(); n != 0 {
		t.Errorf("Disabled backend polled %d times, want 0", n)
	}
}

func TestHealth_SkipHealthCheckNotPolledAndHealthy(t *testing.T) {
	custom, hits := statusServer(t, http.StatusNotFound)
	g := &Gateway{
		backends: []Backend{{Name: "custom", URL: custom.URL, SkipHealthCheck: true, Aliases: []string{"custom"}}},
		health:   newHealthState(),
	}
	g.startHealthChecker()
	if n := hits.Load(); n != 0 {
		t.Errorf("SkipHealthCheck backend polled %d times, want 0", n)
	}
	if !g.IsServiceHealthy("custom") {
		t.Error("a SkipHealthCheck backend reads healthy")
	}
	rr := httptest.NewRecorder()
	g.ModelsHandler()(rr, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if !strings.Contains(rr.Body.String(), `"id":"custom","name":"","description":"","available":true`) {
		t.Errorf("/v1/models must report custom available: %s", rr.Body.String())
	}
	g.backends = append(g.backends, Backend{Name: "canary", Aliases: []string{"canary"}})
	g.defaultModel, g.fallbackModel = "canary", "custom"
	probeResult(g, "canary", false)
	if got := g.ModelForLanguage("cs"); got != "custom" {
		t.Errorf("EU with canary down: got %q, want the custom fallback", got)
	}
}

func TestModelForLanguage_HealthMatrix(t *testing.T) {
	g := &Gateway{
		backends: []Backend{
			{Name: "eng", Aliases: []string{"eng"}},
			{Name: "canary", Aliases: []string{"canary"}},
			{Name: "turbo", Aliases: []string{"turbo"}},
		},
		health:        newHealthState(),
		englishModel:  "eng",
		defaultModel:  "canary",
		fallbackModel: "turbo",
	}
	type up struct{ eng, canary, turbo bool }
	cases := []struct {
		lang string
		h    up
		want string
	}{
		{"en", up{true, true, true}, "eng"},
		{"en", up{false, true, true}, "canary"},
		{"en", up{false, false, true}, "turbo"},
		{"en", up{false, false, false}, "canary"},
		{"", up{false, false, true}, "turbo"},
		{"cs", up{true, true, true}, "canary"},
		{"cs", up{true, false, true}, "turbo"},
		{"cs", up{true, false, false}, "canary"},
		{"ja", up{true, true, true}, "turbo"},
		{"ja", up{true, true, false}, "canary"},
		{"ja", up{true, false, false}, "turbo"},
	}
	for _, c := range cases {
		probeResult(g, "eng", c.h.eng)
		probeResult(g, "canary", c.h.canary)
		probeResult(g, "turbo", c.h.turbo)
		if got := g.ModelForLanguage(c.lang); got != c.want {
			t.Errorf("lang=%q up=%+v: got %q, want %q", c.lang, c.h, got, c.want)
		}
	}
	solo := &Gateway{health: newHealthState(), defaultModel: "canary"}
	if got := solo.ModelForLanguage("ja"); got != "canary" {
		t.Errorf("single backend: got %q, want canary", got)
	}
}

func TestModelForAutoDetect_HealthMatrix(t *testing.T) {
	g := &Gateway{
		health:        newHealthState(),
		defaultModel:  "canary",
		fallbackModel: "turbo",
		parakeetModel: "parakeet",
	}
	csHistory := []LangEntry{{Code: "cs", Count: 20}}
	mixed := []LangEntry{{Code: "cs", Count: 3}, {Code: "de", Count: 3}}
	cases := []struct {
		profile          []LangEntry
		canary, parakeet bool
		wantModel, tier  string
	}{
		{nil, true, true, "turbo", "whisper_safe"},
		{csHistory, true, true, "canary", "canary_confident"},
		{csHistory, false, true, "parakeet", "parakeet_history"},
		{csHistory, false, false, "turbo", "whisper_history"},
		{mixed, true, true, "parakeet", "parakeet_history"},
		{mixed, true, false, "turbo", "whisper_history"},
	}
	for i, c := range cases {
		probeResult(g, "canary", c.canary)
		probeResult(g, "parakeet", c.parakeet)
		r := g.ModelForAutoDetect(AutoDetectContext{Profile: c.profile})
		if r.Model != c.wantModel || r.Tier != c.tier {
			t.Errorf("case %d: got %s/%s, want %s/%s", i, r.Model, r.Tier, c.wantModel, c.tier)
		}
	}
}

func TestModels_AvailableReflectsHealth(t *testing.T) {
	g := &Gateway{
		backends: []Backend{{Name: "a"}, {Name: "b"}},
		health:   newHealthState(),
	}
	probeResult(g, "a", true)
	probeResult(g, "b", false)
	rr := httptest.NewRecorder()
	g.ModelsHandler()(rr, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	body := rr.Body.String()
	if !strings.Contains(body, `"id":"a","name":"","description":"","available":true`) ||
		!strings.Contains(body, `"id":"b","name":"","description":"","available":false`) {
		t.Errorf("want a available, b not, in %s", body)
	}
}

type fakeClock struct{ ns atomic.Int64 }

func useFakeClock(g *Gateway) *fakeClock {
	c := &fakeClock{}
	c.ns.Store(time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC).UnixNano())
	g.health.now = func() time.Time { return time.Unix(0, c.ns.Load()) }
	return c
}

func (c *fakeClock) advance(d time.Duration) { c.ns.Add(int64(d)) }

func TestHealth_PollerCannotLiftLiveTTLDemotion(t *testing.T) {
	withCapturedOnError(t)
	primary, _ := countingBackend(t, http.StatusInternalServerError, "")
	fallback, _ := countingBackend(t, http.StatusOK, "ok")
	srv, g := twoBackendStreamServer(t,
		Backend{Name: "primary", URL: primary.URL, Aliases: []string{"primary"}},
		Backend{Name: "fallback", URL: fallback.URL, Aliases: []string{"fallback"}})
	g.demotionTTL = 120 * time.Second
	clock := useFakeClock(g)
	if _, err := streamPCMAndRead(t, srv); err != nil {
		t.Fatalf("want rescue, got %v", err)
	}
	waitHandlers(t)
	if g.health.get("primary") {
		t.Fatal("primary must be demoted after a 5xx")
	}
	clock.advance(10 * time.Second)
	probeResult(g, "primary", true)
	if g.health.get("primary") {
		t.Error("a probe(ok) inside the TTL must not lift the demotion")
	}
	clock.advance(111 * time.Second)
	if !g.health.get("primary") {
		t.Error("the demotion must end at the TTL when the probe says ok")
	}
}

func TestHealth_FlapAfterRecovery(t *testing.T) {
	g := &Gateway{health: newHealthState(), demotionTTL: 120 * time.Second}
	clock := useFakeClock(g)
	probeResult(g, "turbo", true)
	g.demoteAfterFailure("turbo", "5xx", true)
	clock.advance(10 * time.Second)
	probeResult(g, "turbo", true)
	if g.health.get("turbo") {
		t.Fatal("still demoted at t0+10s")
	}
	clock.advance(50 * time.Second)
	g.demoteAfterFailure("turbo", "5xx", true)
	clock.advance(90 * time.Second)
	if g.health.get("turbo") {
		t.Error("second 5xx must extend the demotion to t0+180s")
	}
	clock.advance(31 * time.Second)
	if !g.health.get("turbo") {
		t.Error("healthy after the extended TTL")
	}
	g.demoteAfterFailure("turbo", "5xx", false)
	if g.health.get("turbo") {
		t.Fatal("no-alternative demotion must take effect")
	}
	probeResult(g, "turbo", true)
	if !g.health.get("turbo") {
		t.Error("no-alternative demotion must end at the next probe(ok)")
	}
}

func TestHTTP5xx_WithAlternative_DemotesForTTL(t *testing.T) {
	primary, _ := countingBackend(t, http.StatusInternalServerError, "")
	fallback, _ := countingBackend(t, http.StatusOK, "ok")
	g := &Gateway{
		backends: []Backend{
			{Name: "primary", URL: primary.URL, Aliases: []string{"primary"}},
			{Name: "fallback", URL: fallback.URL, Aliases: []string{"fallback"}},
		},
		health:        newHealthState(),
		defaultModel:  "primary",
		fallbackModel: "fallback",
		maxBodySize:   10 * 1024 * 1024,
		demotionTTL:   120 * time.Second,
	}
	clock := useFakeClock(g)
	probeResult(g, "primary", true)
	probeResult(g, "fallback", true)
	body, ct := buildMultipart(t, map[string]string{"language": "en"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("want rescue, got %d", rr.Code)
	}
	probeResult(g, "primary", true)
	if g.health.get("primary") {
		t.Error("probe(ok) must not lift a live TTL demotion")
	}
	clock.advance(121 * time.Second)
	if !g.health.get("primary") {
		t.Error("healthy after the TTL")
	}
}

func TestNewGateway_DemotionTTL(t *testing.T) {
	for env, want := range map[string]time.Duration{"": 120 * time.Second, "0": 0, "45": 45 * time.Second, "90s": 90 * time.Second} {
		t.Setenv("DICTION_STT_DEMOTION_TTL", env)
		if got := sttDemotionTTLFromEnv(); got != want {
			t.Errorf("DICTION_STT_DEMOTION_TTL=%q: got %v, want %v", env, got, want)
		}
	}
}

func TestWS_PreRouteGate_RouteFailsWhenPreferredDemoted_Today(t *testing.T) {
	events, _ := withCapturedOnError(t)
	primary, primaryHits := countingBackend(t, http.StatusOK, "ok")
	fallback, _ := countingBackend(t, http.StatusOK, "ok")
	srv, g := twoBackendStreamServer(t,
		Backend{Name: "primary", URL: primary.URL, Aliases: []string{"primary"}},
		Backend{Name: "fallback", URL: fallback.URL, Aliases: []string{"fallback"}})
	probeResult(g, "primary", false)
	probeResult(g, "fallback", false)
	if got := g.ModelForLanguage("en"); got != "primary" {
		t.Fatalf("nothing qualifies: got %q, want the preferred primary", got)
	}
	if !wsRouteRefused(t, srv) {
		t.Fatal("want the WS handshake refused with 503")
	}
	events.WaitFor(1, 2*time.Second)
	if ev := events.All(); len(ev) == 0 || ev[0].Kind != "stt_route_fail" || ev[0].Provider != "primary" {
		t.Errorf("want stt_route_fail for primary, got %+v", ev)
	}
	if primaryHits.Load() != 0 {
		t.Error("WS must not call a demoted backend")
	}

	body, ct := buildMultipart(t, map[string]string{"language": "en"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)
	if rr.Code != http.StatusOK || primaryHits.Load() != 1 {
		t.Errorf("HTTP still sends to the preferred model: code=%d hits=%d", rr.Code, primaryHits.Load())
	}
}

func singleBackendStreamServer(t *testing.T, b Backend) (*httptest.Server, *Gateway) {
	t.Helper()
	g := &Gateway{
		backends:     []Backend{b},
		health:       newHealthState(),
		defaultModel: b.Name,
		maxBodySize:  10 * 1024 * 1024,
		demotionTTL:  120 * time.Second,
	}
	g.health.set(b.Name, true)
	srv := httptest.NewServer(trackHandler(g.StreamingHandler()))
	t.Cleanup(srv.Close)
	return srv, g
}

func TestWS_SingleBackend5xx_DemotedUntilNextProbe(t *testing.T) {
	events, _ := withCapturedOnError(t)
	var status atomic.Int32
	status.Store(http.StatusInternalServerError)
	var hits atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(int(status.Load()))
		if status.Load() == http.StatusOK {
			w.Write([]byte(`{"text":"back"}`))
		}
	}))
	t.Cleanup(backend.Close)
	srv, g := singleBackendStreamServer(t, Backend{Name: "solo", URL: backend.URL, Aliases: []string{"solo"}})

	if _, err := streamPCMAndRead(t, srv); err == nil {
		t.Fatal("want the 5xx dictation to fail")
	}
	waitHandlers(t)
	if g.health.get("solo") {
		t.Fatal("a 5xx keeps its demotion")
	}
	status.Store(http.StatusOK)
	if !wsRouteRefused(t, srv) {
		t.Fatal("want route fail (503) while demoted")
	}
	waitHandlers(t)
	if n := hits.Load(); n != 1 {
		t.Errorf("demoted backend called again: hits=%d", n)
	}
	sawRouteFail := false
	for _, e := range events.All() {
		if e.Kind == "stt_route_fail" {
			sawRouteFail = true
		}
	}
	if !sawRouteFail {
		t.Error("want stt_route_fail while demoted")
	}
	probeResult(g, "solo", true)
	res, err := streamPCMAndRead(t, srv)
	if err != nil || res["text"] != "back" {
		t.Errorf("after probe(ok) want the backend again, got %v %v", res, err)
	}
}

func TestWS_UndoKeepsPollerDown(t *testing.T) {
	withCapturedOnError(t)
	var g *Gateway
	backend := transportFaultBackend(t, func() { probeResult(g, "solo", false) })
	var srv *httptest.Server
	srv, g = singleBackendStreamServer(t, Backend{Name: "solo", URL: backend.URL, Aliases: []string{"solo"}})
	if _, err := streamPCMAndRead(t, srv); err == nil {
		t.Fatal("want the transport-fault dictation to fail")
	}
	waitHandlers(t)
	if g.health.get("solo") {
		t.Error("the undo must not override the poller's down verdict")
	}
	probeResult(g, "solo", true)
	if !g.health.get("solo") {
		t.Error("healthy again once the poller says ok")
	}
}

func TestHealth_UndoClearsOnlyItsOwnDemotion(t *testing.T) {
	h := newHealthState()
	h.probe("solo", true)
	transport := h.demote("solo", "transport", 0)
	h.demote("solo", "5xx", 0)
	h.undoDemotion("solo", transport)
	if h.get("solo") {
		t.Error("the other request's 5xx demotion must stand")
	}
	h.probe("solo", true)
	stale := h.demote("solo", "transport", 0)
	h.probe("solo", true)
	h.demote("solo", "5xx", 0)
	h.undoDemotion("solo", stale)
	if h.get("solo") {
		t.Error("a stale undo must not clear a demotion made after the probe")
	}
	h.probe("solo", true)
	own := h.demote("solo", "transport", 0)
	h.undoDemotion("solo", own)
	if !h.get("solo") {
		t.Error("undo of the only demotion restores the backend")
	}
}
