package core

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
)

// Merge on the batch socket (diction-routes C3/C6, plan 01c).
//
// A Diction build on a merged route opens the socket with `merge=1&phones=nemotron[,parakeet]`,
// sends a plain `{"action":"done"}` at stop — cloud STT starts at once — and then one
// `{"action":"device_transcript","model":…,"text":…}` frame per phone model as each finishes.
// The gateway keeps reading after `done`, waits for every model named in `phones=` but no
// longer than its own STT plus mergeDeviceGrace, and runs one LLM call (MergeProcess) that chooses between what the
// cloud and the phone heard. Old builds never send `merge=1`; old gateways never read the
// follow-up frames. Without `merge=1` nothing here runs.

// MergeProcess is the merge Writing Tools pass: the cloud transcript plus the phone
// transcripts that arrived ({"parakeet": …, "nemotron": …}, at least one), returning
// (text, mode). Set by the cloud gateway (main.go); nil in the community build, where
// `merge=1` is ignored.
var MergeProcess func(ctx context.Context, cloudText string, phone map[string]string, contextJSON string) (string, string, error)

// OnMerged reports, once per eligible `merge=1` session, whether the result is a merge
// (true) or today's cleanup/raw path (false: no phone text arrived in time, the cloud
// transcript was empty, or the merge failed). Nil-safe.
var OnMerged func(ctx context.Context, merged bool)

// OnMergeInputs reports, once per eligible `merge=1` session after the wait, which phone
// models' non-empty transcripts the gateway has (sorted; empty when none) and how long it
// waited past the end of cloud STT. Nil-safe.
var OnMergeInputs func(ctx context.Context, phones []string, waited time.Duration)

// mergeDeviceGrace is how long past the end of cloud STT the gateway waits for the phone
// transcripts the client named in `phones=` (it stops early once all are in). Measured on
// Czech (2026-10-12): Parakeet on an iPhone lands ~0.73 s after stop, cloud STT ends ~0.5 s
// after it, so the first value (300 ms) gave 185 of 186 merges only Nemotron. 1.2 s keeps
// the worst case (STT p90 0.7 s + 1.2 s + the merge call) inside the 4 s stop→final target.
// Env DICTION_MERGE_DEVICE_GRACE_MS, clamped to 0–3000. A var for tests.
var mergeDeviceGrace = mergeDeviceGraceFromEnv()

func mergeDeviceGraceFromEnv() time.Duration {
	ms := min(max(EnvIntOrDefault("DICTION_MERGE_DEVICE_GRACE_MS", 1200), 0), 3000)
	return time.Duration(ms) * time.Millisecond
}

// mergeFallbackMinBudget is the least of the merge pass's budget that must remain for a
// guard-rejected merge to fall back to the normal cleanup of the cloud text (successful
// cleanups take a median ~0.54 s); with less, the cloud raw goes out as before.
const mergeFallbackMinBudget = 800 * time.Millisecond

// MergeRejectedError is a merge answer a guard refused, carrying the merge pass's deadline so
// the socket can still run the normal cleanup in what is left. It unwraps to
// ErrEnhanceRejected (never retried, enhance_skipped=rejected).
type MergeRejectedError struct {
	Deadline time.Time // zero when the pass had none
}

func (e *MergeRejectedError) Error() string { return ErrEnhanceRejected.Error() }
func (e *MergeRejectedError) Unwrap() error { return ErrEnhanceRejected }

// maxDeviceTranscriptBytes caps one phone transcript (the client truncates at the same size).
const maxDeviceTranscriptBytes = 64 << 10

// mergePhoneModels is the closed set of phone models a device_transcript frame may name,
// in the order the empty-cloud fallback prefers them (Parakeet is usually the more accurate).
var mergePhoneModels = []string{"parakeet", "nemotron"}

func isMergePhoneModel(m string) bool {
	for _, k := range mergePhoneModels {
		if k == m {
			return true
		}
	}
	return false
}

// expectedPhoneTranscripts reads `phones=` (comma list): how many distinct known models the
// client said it will send. Absent or nothing known → 1.
func expectedPhoneTranscripts(phones string) int {
	seen := map[string]bool{}
	for _, p := range strings.Split(phones, ",") {
		if p = strings.TrimSpace(p); isMergePhoneModel(p) {
			seen[p] = true
		}
	}
	if len(seen) == 0 {
		return 1
	}
	return len(seen)
}

// deviceTranscripts collects device_transcript frames read after `done`.
type deviceTranscripts struct {
	mu      sync.Mutex
	texts   map[string]string
	want    int
	changed chan struct{} // poked (non-blocking) on every new model
	closed  chan struct{} // the reader has exited (socket closed or failed)
}

// startDeviceTranscriptReader reads the socket in a goroutine until it fails or closes,
// keeping the first device_transcript per known model. Everything else is ignored. The
// handler's deferred CloseNow ends it.
func startDeviceTranscriptReader(ctx context.Context, conn *websocket.Conn, want int) *deviceTranscripts {
	d := &deviceTranscripts{
		texts:   map[string]string{},
		want:    want,
		changed: make(chan struct{}, 1),
		closed:  make(chan struct{}),
	}
	go func() {
		defer close(d.closed)
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			var a streamAction
			if json.Unmarshal(data, &a) != nil || a.Action != "device_transcript" || !isMergePhoneModel(a.Model) {
				continue
			}
			text := strings.TrimSpace(truncateUTF8(a.Text, maxDeviceTranscriptBytes))
			d.mu.Lock()
			if _, dup := d.texts[a.Model]; !dup {
				d.texts[a.Model] = text
			}
			d.mu.Unlock()
			select {
			case d.changed <- struct{}{}:
			default:
			}
		}
	}()
	return d
}

// wait returns the non-empty transcripts that arrived by deadline, returning early once every
// expected model has sent its frame or the socket is gone.
func (d *deviceTranscripts) wait(deadline time.Time) map[string]string {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
wait:
	for {
		d.mu.Lock()
		n := len(d.texts)
		d.mu.Unlock()
		if n >= d.want {
			break
		}
		select {
		case <-d.changed:
		case <-timer.C:
			break wait
		case <-d.closed:
			break wait
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[string]string, len(d.texts))
	for m, t := range d.texts {
		if t != "" {
			out[m] = t
		}
	}
	return out
}

// bestPhoneTranscript is the phone text the empty-cloud fallback cleans up.
func bestPhoneTranscript(phone map[string]string) string {
	for _, m := range mergePhoneModels {
		if t := phone[m]; t != "" {
			return t
		}
	}
	return ""
}

// truncateUTF8 cuts s to at most n bytes without splitting a rune.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func reportMerged(ctx context.Context, merged bool) {
	if OnMerged != nil {
		OnMerged(ctx, merged)
	}
}

func isEditIntentParam(intent string) bool {
	return intent == "edit" || intent == "edit-selected"
}

// mergeInline is the at-stop merge decision. handled=true means the returned text is the
// merge result (or the cloud raw after a failed merge) and the caller's cleanup pass must not
// run. handled=false hands back the text the caller's usual cleanup pass should run on: the
// cloud transcript, or — when the cloud heard nothing — the best phone transcript. A non-zero
// cleanupBy (a guard-rejected merge with budget left) is the deadline that cleanup must meet.
func mergeInline(ctx context.Context, cloud string, phone map[string]string, contextJSON string) (text, mode string, handled bool, cleanupBy time.Time) {
	if len(phone) == 0 {
		reportMerged(ctx, false)
		return cloud, "", false, time.Time{}
	}
	if cloud == "" {
		reportMerged(ctx, false)
		return bestPhoneTranscript(phone), "", false, time.Time{}
	}
	out, m, err := MergeProcess(ctx, cloud, phone, contextJSON)
	if err != nil {
		log.Printf("ws merge: %v", err)
		reportMerged(ctx, false)
		var rej *MergeRejectedError
		if errors.As(err, &rej) && !rej.Deadline.IsZero() && time.Until(rej.Deadline) >= mergeFallbackMinBudget {
			// The merge disagreed with its guard, not with the clock: the cloud text still
			// gets today's cleanup, inside what is left of the merge's budget.
			log.Printf("ws merge: rejected, cleaning up the cloud transcript (%v left)", time.Until(rej.Deadline).Round(time.Millisecond))
			return cloud, "", false, rej.Deadline
		}
		// Exactly like a failed cleanup today: the cloud raw goes out with no mode, and
		// the skip is recorded on the request's own row.
		ReportEnhanceSkipped(ctx, EnhanceSkipReason(ctx, err))
		return cloud, "", true, time.Time{}
	}
	reportMerged(ctx, true)
	return out, m, true, time.Time{}
}

// waitAndReport waits for the phone transcripts (until sttDoneAt + mergeDeviceGrace)
// and reports what arrived to OnMergeInputs.
func (d *deviceTranscripts) waitAndReport(ctx context.Context, sttDoneAt time.Time) map[string]string {
	phone := d.wait(sttDoneAt.Add(mergeDeviceGrace))
	if OnMergeInputs != nil {
		models := make([]string, 0, len(phone))
		for m := range phone {
			models = append(models, m)
		}
		sort.Strings(models)
		OnMergeInputs(ctx, models, max(time.Since(sttDoneAt), 0))
	}
	return phone
}
