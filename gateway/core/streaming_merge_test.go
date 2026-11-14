package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Merge on the batch socket (C3, plan 01c): plain done → STT at once ∥ device_transcript
// frames → merge (or today's cleanup) inline → one result frame.

type mergeRig struct {
	mu          sync.Mutex
	sttHitAt    time.Time
	mergeCalls  []map[string]string
	mergeCloud  string
	cleanupText []string
	cleanupMode []string
	mergedSeen  []bool
	skipped     []string
	cleanupBy   []time.Time
	inputs      [][]string
	waited      []time.Duration
}

func (m *mergeRig) merges() []map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]map[string]string(nil), m.mergeCalls...)
}

func (m *mergeRig) cleanups() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.cleanupText...)
}

// startMergeServer runs the handler against a fake STT that answers transcript after
// sttDelay. mergeErr makes MergeProcess fail; noHook leaves it nil (community build).
func startMergeServer(t *testing.T, transcript string, sttDelay time.Duration, mergeErr error, noHook bool) (*httptest.Server, *mergeRig) {
	t.Helper()
	rig := &mergeRig{}
	whisper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rig.mu.Lock()
		rig.sttHitAt = time.Now()
		rig.mu.Unlock()
		time.Sleep(sttDelay)
		fmt.Fprintf(w, `{"text":"%s"}`, transcript)
	}))
	t.Cleanup(whisper.Close)

	g := &Gateway{
		backends:     []Backend{{Name: "small", URL: whisper.URL, Aliases: []string{"small"}}},
		health:       newHealthState(),
		defaultModel: "small",
		maxBodySize:  10 * 1024 * 1024,
	}
	g.health.set("small", true)

	waitHandlers(t) // previous test's handlers may still read the hooks from their last defer
	prevMerge, prevOnMerged, prevSkip, prevInputs, prevGrace := MergeProcess, OnMerged, OnEnhanceSkipped, OnMergeInputs, mergeDeviceGrace
	t.Cleanup(func() {
		waitHandlers(t)
		MergeProcess, OnMerged, OnEnhanceSkipped, OnMergeInputs, mergeDeviceGrace = prevMerge, prevOnMerged, prevSkip, prevInputs, prevGrace
	})
	mergeDeviceGrace = 300 * time.Millisecond // the timing tests are written against the grace, whatever its value
	OnMergeInputs = func(_ context.Context, phones []string, waited time.Duration) {
		rig.mu.Lock()
		rig.inputs = append(rig.inputs, phones)
		rig.waited = append(rig.waited, waited)
		rig.mu.Unlock()
	}
	MergeProcess = func(_ context.Context, cloud string, phone map[string]string, _ string) (string, string, error) {
		rig.mu.Lock()
		defer rig.mu.Unlock()
		rig.mergeCalls = append(rig.mergeCalls, phone)
		rig.mergeCloud = cloud
		if mergeErr != nil {
			return "", "", mergeErr
		}
		return "merged:" + cloud, "transcribe", nil
	}
	if noHook {
		MergeProcess = nil
	}
	OnMerged = func(_ context.Context, merged bool) {
		rig.mu.Lock()
		rig.mergedSeen = append(rig.mergedSeen, merged)
		rig.mu.Unlock()
	}
	OnEnhanceSkipped = func(_ context.Context, reason string) {
		rig.mu.Lock()
		rig.skipped = append(rig.skipped, reason)
		rig.mu.Unlock()
	}
	cleanup := func(ctx context.Context, text, _, intent string) (string, string, error) {
		rig.mu.Lock()
		if d, ok := ctx.Deadline(); ok {
			rig.cleanupBy = append(rig.cleanupBy, d)
		}
		rig.cleanupText = append(rig.cleanupText, text)
		rig.cleanupMode = append(rig.cleanupMode, intent)
		rig.mu.Unlock()
		if intent == "edit" {
			return "edited", "edit", nil
		}
		return "cleaned:" + text, "transcribe", nil
	}
	srv := httptest.NewServer(trackHandler(g.StreamingHandlerWithSplitEnhance(cleanup, nil)))
	t.Cleanup(srv.Close)
	return srv, rig
}

type phoneFrame struct {
	after time.Duration // after done
	model string
	text  string
}

// dialMerge sends audio, done, then each phone frame at its offset (from a goroutine, so
// the result can arrive meanwhile), and returns the text frames and when done was sent.
func dialMerge(t *testing.T, srv *httptest.Server, query string, frames []phoneFrame) ([]string, time.Time, time.Time) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(srv, query), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageBinary, make([]byte, 3200)); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	doneAt := time.Now()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"action":"done"}`)); err != nil {
		t.Fatalf("write done: %v", err)
	}
	go func() {
		for _, f := range frames {
			time.Sleep(time.Until(doneAt.Add(f.after)))
			b, _ := json.Marshal(map[string]string{"action": "device_transcript", "model": f.model, "text": f.text})
			_ = conn.Write(ctx, websocket.MessageText, b)
		}
	}()
	var out []string
	var resultAt time.Time
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return out, doneAt, resultAt
		}
		if resultAt.IsZero() {
			resultAt = time.Now()
		}
		out = append(out, string(data))
	}
}

func oneResult(t *testing.T, frames []string) streamResult {
	t.Helper()
	if len(frames) != 1 {
		t.Fatalf("want exactly one result frame, got %d: %v", len(frames), frames)
	}
	var r streamResult
	if err := json.Unmarshal([]byte(frames[0]), &r); err != nil {
		t.Fatalf("result frame: %v", err)
	}
	return r
}

const mergeQuery = "model=small&language=cs&enhance=true&merge=1&diction_route=merged"

func TestMerge_OneFrame(t *testing.T) {
	srv, rig := startMergeServer(t, "cloud text", 0, nil, false)
	frames, _, _ := dialMerge(t, srv, mergeQuery+"&phones=nemotron", []phoneFrame{{0, "nemotron", "nemo text"}})
	r := oneResult(t, frames)
	if r.Text != "merged:cloud text" || r.Mode != "transcribe" {
		t.Errorf("result = %+v, want the merge output", r)
	}
	m := rig.merges()
	if len(m) != 1 || m[0]["nemotron"] != "nemo text" || len(m[0]) != 1 {
		t.Errorf("merge calls = %v", m)
	}
	if len(rig.cleanups()) != 0 {
		t.Errorf("cleanup must not run after a merge: %v", rig.cleanups())
	}
	if len(rig.mergedSeen) != 1 || !rig.mergedSeen[0] {
		t.Errorf("OnMerged = %v, want [true]", rig.mergedSeen)
	}
}

func TestMerge_TwoFramesDuringSTT(t *testing.T) {
	srv, rig := startMergeServer(t, "cloud text", 200*time.Millisecond, nil, false)
	frames, doneAt, resultAt := dialMerge(t, srv, mergeQuery+"&phones=nemotron,parakeet", []phoneFrame{
		{10 * time.Millisecond, "nemotron", "nemo"},
		{50 * time.Millisecond, "parakeet", "para"},
	})
	oneResult(t, frames)
	m := rig.merges()
	if len(m) != 1 || m[0]["nemotron"] != "nemo" || m[0]["parakeet"] != "para" {
		t.Errorf("merge calls = %v, want both phone transcripts", m)
	}
	// Both arrived before STT ended: the grace is not spent.
	if d := resultAt.Sub(doneAt); d > 200*time.Millisecond+mergeDeviceGrace-50*time.Millisecond {
		t.Errorf("result after %v; with every frame in, the gateway must not wait out the grace", d)
	}
}

func TestMerge_FrameAfterGraceIgnored(t *testing.T) {
	srv, rig := startMergeServer(t, "cloud text", 20*time.Millisecond, nil, false)
	frames, doneAt, resultAt := dialMerge(t, srv, mergeQuery+"&phones=nemotron,parakeet", []phoneFrame{
		{0, "nemotron", "nemo"},
		{20*time.Millisecond + mergeDeviceGrace + 400*time.Millisecond, "parakeet", "late"},
	})
	oneResult(t, frames)
	m := rig.merges()
	if len(m) != 1 || len(m[0]) != 1 || m[0]["nemotron"] != "nemo" {
		t.Errorf("merge calls = %v, want only the earlier frame", m)
	}
	if d := resultAt.Sub(doneAt); d > 20*time.Millisecond+mergeDeviceGrace+250*time.Millisecond {
		t.Errorf("result after %v: the wait must end at STT + grace", d)
	}
}

func TestMerge_NoFrames_TodaysCleanup(t *testing.T) {
	srv, rig := startMergeServer(t, "cloud text", 0, nil, false)
	frames, doneAt, resultAt := dialMerge(t, srv, mergeQuery+"&phones=nemotron", nil)
	r := oneResult(t, frames)
	if r.Text != "cleaned:cloud text" {
		t.Errorf("result = %+v, want cleanup of the cloud transcript", r)
	}
	if len(rig.merges()) != 0 {
		t.Errorf("no phone text → no merge, got %v", rig.merges())
	}
	if len(rig.mergedSeen) != 1 || rig.mergedSeen[0] {
		t.Errorf("OnMerged = %v, want [false]", rig.mergedSeen)
	}
	if d := resultAt.Sub(doneAt); d > mergeDeviceGrace+400*time.Millisecond {
		t.Errorf("result after %v: never wait longer than STT + grace", d)
	}
}

func TestMerge_STTNotDelayedByLateFrames(t *testing.T) {
	srv, rig := startMergeServer(t, "cloud text", 0, nil, false)
	_, doneAt, _ := dialMerge(t, srv, mergeQuery+"&phones=nemotron", []phoneFrame{{250 * time.Millisecond, "nemotron", "nemo"}})
	rig.mu.Lock()
	hit := rig.sttHitAt
	rig.mu.Unlock()
	if d := hit.Sub(doneAt); d > 150*time.Millisecond {
		t.Errorf("cloud STT started %v after done; it must never wait for the phone", d)
	}
}

func TestMerge_EmptyCloud_CleansBestPhone(t *testing.T) {
	srv, rig := startMergeServer(t, "", 0, nil, false)
	frames, _, _ := dialMerge(t, srv, mergeQuery+"&phones=nemotron,parakeet", []phoneFrame{
		{0, "nemotron", "nemo"}, {5 * time.Millisecond, "parakeet", "para"},
	})
	r := oneResult(t, frames)
	if r.Text != "cleaned:para" {
		t.Errorf("result = %+v, want cleanup of Parakeet's text", r)
	}
	if len(rig.merges()) != 0 {
		t.Errorf("empty cloud must not merge: %v", rig.merges())
	}
	if len(rig.mergedSeen) != 1 || rig.mergedSeen[0] {
		t.Errorf("OnMerged = %v, want [false]", rig.mergedSeen)
	}
}

func TestMerge_NilHook_TodaysCleanup(t *testing.T) {
	srv, rig := startMergeServer(t, "cloud text", 0, nil, true)
	frames, _, _ := dialMerge(t, srv, mergeQuery+"&phones=nemotron", []phoneFrame{{0, "nemotron", "nemo"}})
	if r := oneResult(t, frames); r.Text != "cleaned:cloud text" {
		t.Errorf("result = %+v, want today's cleanup", r)
	}
	if len(rig.mergedSeen) != 0 {
		t.Errorf("community build reports no merge outcome, got %v", rig.mergedSeen)
	}
}

func TestMerge_EnhanceOff_RawOnly(t *testing.T) {
	srv, rig := startMergeServer(t, "cloud text", 0, nil, false)
	frames, _, _ := dialMerge(t, srv, "model=small&language=cs&merge=1&phones=nemotron", []phoneFrame{{0, "nemotron", "nemo"}})
	if r := oneResult(t, frames); r.Text != "cloud text" || r.Mode != "" {
		t.Errorf("result = %+v, want the raw cloud transcript", r)
	}
	if len(rig.merges())+len(rig.cleanups()) != 0 {
		t.Errorf("enhance off: no LLM pass, got merges=%v cleanups=%v", rig.merges(), rig.cleanups())
	}
}

func TestMerge_EditIntent_NoMerge(t *testing.T) {
	srv, rig := startMergeServer(t, "make it shorter", 0, nil, false)
	frames, _, _ := dialMerge(t, srv, mergeQuery+"&phones=nemotron&intent=edit", []phoneFrame{{0, "nemotron", "nemo"}})
	if r := oneResult(t, frames); r.Mode != "edit" {
		t.Errorf("result = %+v, want the edit", r)
	}
	if len(rig.merges()) != 0 {
		t.Errorf("edit intent must not merge: %v", rig.merges())
	}
}

func TestMerge_AutoEditWins(t *testing.T) {
	prev := ResolveIntent
	t.Cleanup(func() { ResolveIntent = prev })
	ResolveIntent = func(context.Context, *http.Request, string, string, string) (string, bool) { return "edit", true }
	srv, rig := startMergeServer(t, "make it shorter", 0, nil, false)
	frames, _, _ := dialMerge(t, srv, mergeQuery+"&phones=nemotron&auto_edit=1", []phoneFrame{{0, "nemotron", "nemo"}})
	if len(rig.merges()) != 0 {
		t.Errorf("Auto-edit must win over the merge: %v", rig.merges())
	}
	last := frames[len(frames)-1]
	if !strings.Contains(last, `"mode":"edit"`) {
		t.Errorf("last frame = %s, want the edit result", last)
	}
}

func TestMerge_Error_CloudRaw(t *testing.T) {
	srv, rig := startMergeServer(t, "cloud text", 0, errors.New("llm down"), false)
	frames, _, _ := dialMerge(t, srv, mergeQuery+"&phones=nemotron", []phoneFrame{{0, "nemotron", "nemo"}})
	r := oneResult(t, frames)
	if r.Text != "cloud text" || r.Mode != "" || r.Status != "" {
		t.Errorf("result = %+v, want the cloud raw exactly like a failed cleanup", r)
	}
	if len(rig.cleanups()) != 0 {
		t.Errorf("a failed merge has spent the budget; no second pass: %v", rig.cleanups())
	}
	if len(rig.skipped) != 1 {
		t.Errorf("enhance skip not recorded: %v", rig.skipped)
	}
	if len(rig.mergedSeen) != 1 || rig.mergedSeen[0] {
		t.Errorf("OnMerged = %v, want [false]", rig.mergedSeen)
	}
}

func TestMerge_GuardRejected_FallsBackToCleanup(t *testing.T) {
	rej := &MergeRejectedError{Deadline: time.Now().Add(5 * time.Second)}
	srv, rig := startMergeServer(t, "cloud text", 0, rej, false)
	frames, _, _ := dialMerge(t, srv, mergeQuery+"&phones=nemotron", []phoneFrame{{0, "nemotron", "nemo"}})
	r := oneResult(t, frames)
	if r.Text != "cleaned:cloud text" || r.Mode != "transcribe" {
		t.Errorf("result = %+v, want today's cleanup of the cloud text", r)
	}
	if len(rig.skipped) != 0 {
		t.Errorf("a cleaned fallback is not a skip: %v", rig.skipped)
	}
	if len(rig.mergedSeen) != 1 || rig.mergedSeen[0] {
		t.Errorf("OnMerged = %v, want [false]", rig.mergedSeen)
	}
}

func TestMerge_GuardRejected_NoBudget_CloudRaw(t *testing.T) {
	rej := &MergeRejectedError{Deadline: time.Now().Add(100 * time.Millisecond)}
	srv, rig := startMergeServer(t, "cloud text", 0, rej, false)
	frames, _, _ := dialMerge(t, srv, mergeQuery+"&phones=nemotron", []phoneFrame{{0, "nemotron", "nemo"}})
	r := oneResult(t, frames)
	if r.Text != "cloud text" || r.Mode != "" {
		t.Errorf("result = %+v, want the cloud raw", r)
	}
	if len(rig.cleanups()) != 0 {
		t.Errorf("no budget left: no cleanup, got %v", rig.cleanups())
	}
	if len(rig.skipped) != 1 || rig.skipped[0] != EnhanceSkipRejected {
		t.Errorf("skip = %v, want [rejected]", rig.skipped)
	}
}

func TestMerge_GuardRejected_CleanupMeetsMergeDeadline(t *testing.T) {
	deadline := time.Now().Add(4 * time.Second)
	srv, rig := startMergeServer(t, "cloud text", 0, &MergeRejectedError{Deadline: deadline}, false)
	dialMerge(t, srv, mergeQuery+"&phones=nemotron", []phoneFrame{{0, "nemotron", "nemo"}})
	rig.mu.Lock()
	defer rig.mu.Unlock()
	if len(rig.cleanupBy) != 1 || !rig.cleanupBy[0].Equal(deadline) {
		t.Errorf("cleanup deadline = %v, want the merge pass's %v", rig.cleanupBy, deadline)
	}
}

func TestMerge_InputsReported(t *testing.T) {
	srv, rig := startMergeServer(t, "cloud text", 0, nil, false)
	dialMerge(t, srv, mergeQuery+"&phones=nemotron,parakeet", []phoneFrame{
		{0, "parakeet", "para"}, {5 * time.Millisecond, "nemotron", "nemo"},
	})
	rig.mu.Lock()
	defer rig.mu.Unlock()
	if len(rig.inputs) != 1 || strings.Join(rig.inputs[0], ",") != "nemotron,parakeet" {
		t.Errorf("OnMergeInputs phones = %v, want [nemotron parakeet]", rig.inputs)
	}
	if rig.waited[0] > 250*time.Millisecond {
		t.Errorf("waited %v with every frame in", rig.waited[0])
	}
}

func TestMerge_InputsReported_None(t *testing.T) {
	srv, rig := startMergeServer(t, "cloud text", 0, nil, false)
	dialMerge(t, srv, mergeQuery+"&phones=nemotron", nil)
	rig.mu.Lock()
	defer rig.mu.Unlock()
	if len(rig.inputs) != 1 || len(rig.inputs[0]) != 0 {
		t.Errorf("OnMergeInputs phones = %v, want one empty report", rig.inputs)
	}
	if rig.waited[0] < mergeDeviceGrace-20*time.Millisecond {
		t.Errorf("waited %v, want the full grace", rig.waited[0])
	}
}

func TestMergeDeviceGraceFromEnv(t *testing.T) {
	for _, tc := range []struct {
		env  string
		want time.Duration
	}{{"", 1200 * time.Millisecond}, {"500", 500 * time.Millisecond}, {"-5", 0}, {"99999", 3 * time.Second}, {"x", 1200 * time.Millisecond}} {
		t.Setenv("DICTION_MERGE_DEVICE_GRACE_MS", tc.env)
		if got := mergeDeviceGraceFromEnv(); got != tc.want {
			t.Errorf("env %q → %v, want %v", tc.env, got, tc.want)
		}
	}
}

func TestMerge_SplitEnhanceIgnoresMerge(t *testing.T) {
	srv, rig := startMergeServer(t, "cloud text", 0, nil, false)
	frames, _, _ := dialMerge(t, srv, mergeQuery+"&phones=nemotron&split_enhance=true", []phoneFrame{{0, "nemotron", "nemo"}})
	if len(frames) != 2 {
		t.Fatalf("split answer: want 2 frames, got %v", frames)
	}
	if len(rig.merges()) != 0 {
		t.Errorf("split_enhance must ignore merge=1: %v", rig.merges())
	}
}

// Without merge=1 the session is byte-identical to today, hook installed or not, even if a
// client sent device_transcript frames anyway.
func TestMerge_NoMergeParam_Golden(t *testing.T) {
	srv, rig := startMergeServer(t, "cloud text", 0, nil, false)
	frames, _, _ := dialMerge(t, srv, "model=small&language=cs&enhance=true&phones=nemotron", []phoneFrame{{0, "nemotron", "nemo"}})
	if len(frames) != 1 || frames[0] != `{"text":"cleaned:cloud text","mode":"transcribe"}` {
		t.Errorf("frames = %v, want today's single cleanup frame", frames)
	}
	if len(rig.merges()) != 0 || len(rig.mergedSeen) != 0 {
		t.Errorf("no merge=1: merges=%v merged=%v", rig.merges(), rig.mergedSeen)
	}
}

func TestDeviceTranscriptHelpers(t *testing.T) {
	for in, want := range map[string]int{"": 1, "nemotron": 1, "nemotron,parakeet": 2, "parakeet,parakeet": 1, "whisper": 1, "nemotron, parakeet,x": 2} {
		if got := expectedPhoneTranscripts(in); got != want {
			t.Errorf("expectedPhoneTranscripts(%q) = %d, want %d", in, got, want)
		}
	}
	if got := truncateUTF8("ab"+strings.Repeat("ř", 3), 5); got != "abř" {
		t.Errorf("truncateUTF8 split a rune: %q", got)
	}
	if got := bestPhoneTranscript(map[string]string{"nemotron": "n", "parakeet": "p"}); got != "p" {
		t.Errorf("bestPhoneTranscript = %q, want Parakeet's", got)
	}
}
