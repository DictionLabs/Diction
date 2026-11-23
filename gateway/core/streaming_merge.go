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

var MergeProcess func(ctx context.Context, cloudText string, phone map[string]string, contextJSON string) (string, string, error)

var OnMerged func(ctx context.Context, merged bool)

var OnMergeInputs func(ctx context.Context, phones []string, waited time.Duration)

var mergeDeviceGrace = mergeDeviceGraceFromEnv()

func mergeDeviceGraceFromEnv() time.Duration {
	ms := min(max(EnvIntOrDefault("DICTION_MERGE_DEVICE_GRACE_MS", 1200), 0), 3000)
	return time.Duration(ms) * time.Millisecond
}

const mergeFallbackMinBudget = 800 * time.Millisecond

type MergeRejectedError struct {
	Deadline time.Time
}

func (e *MergeRejectedError) Error() string { return ErrEnhanceRejected.Error() }
func (e *MergeRejectedError) Unwrap() error { return ErrEnhanceRejected }

const maxDeviceTranscriptBytes = 64 << 10

var mergePhoneModels = []string{nameParakeet, "nemotron"}

func isMergePhoneModel(m string) bool {
	for _, k := range mergePhoneModels {
		if k == m {
			return true
		}
	}
	return false
}

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

type deviceTranscripts struct {
	mu      sync.Mutex
	texts   map[string]string
	want    int
	changed chan struct{}
	closed  chan struct{}
}

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

func bestPhoneTranscript(phone map[string]string) string {
	for _, m := range mergePhoneModels {
		if t := phone[m]; t != "" {
			return t
		}
	}
	return ""
}

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
	return intent == intentEdit || intent == intentEditSelected
}

func mergeInline(
	ctx context.Context, cloud string, phone map[string]string, contextJSON string,
) (text, mode string, handled bool, cleanupBy time.Time) {
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
			log.Printf("ws merge: rejected, cleaning up the cloud transcript (%v left)", time.Until(rej.Deadline).Round(time.Millisecond))
			return cloud, "", false, rej.Deadline
		}
		ReportEnhanceSkipped(ctx, EnhanceSkipReason(ctx, err))
		return cloud, "", true, time.Time{}
	}
	reportMerged(ctx, true)
	return out, m, true, time.Time{}
}

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
