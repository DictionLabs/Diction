package core

import "context"

// ErrorEvent is a structured error observation emitted by the gateway.
//
// Public API: this is the contract between core/ (open-source) and private
// gateway wiring (metrics.go, which writes to InfluxDB). Community builds
// leave OnError == nil and the struct is inert.
//
// Stability: treat this struct as public API — field renames or removals
// require a major-version bump of the gateway module. Adding new optional
// fields is additive and safe.
//
// PII rule: callers MUST set Hint to a short, human-curated description
// — never err.Error() verbatim. See plan-review concern #3 in
// .claude/influxdb-gateway-logging-plan.md.
type ErrorEvent struct {
	Source       string // closed vocabulary: middleware | llm | e2e | auth | stt | streaming | startup | panic | ios_app | ios_keyboard
	Kind         string // closed vocabulary per source — e.g. jws_chain, llm_timeout
	Reason       string // optional closed-vocabulary sub-classifier (e.g. ws close reason). Empty = not set, omitted from tags.
	Endpoint     string // HTTP path, e.g. /v1/audio/transcriptions
	Provider     string // upstream provider name, empty if N/A
	ProviderCode string // structured upstream code, empty if N/A
	HTTPStatus   int    // HTTP status returned to client
	Hint         string // short curated description, ≤200 chars, never raw err.Error()
	InputChars   int    // size of input in chars, never content
	LatencyMs    int64
	DeviceHash   string // SHA-256 hex of device id, empty if anonymous

	// Optional iOS error fields (source=ios_app|ios_keyboard). Each emitted
	// as an Influx field when non-zero / non-empty so we never pollute the
	// errors measurement with always-blank columns for gateway-internal sources.
	LastOperation         string // closed enum: kbkill_heartbeat_stale only — kb_op_*
	TerminationReason     string // closed enum: kbkill_metrickit_crash only — oom|watchdog|signal|other|unknown
	PhysFootprintMB       int64
	RSSMB                 int64
	SecondsSinceHeartbeat int64
	Signal                int64
	HangDurationMs        int64
	PeakMemoryMB          int64
	OSStatus              int64
	ModelID               string // closed enum populated per model_load_failed
	AliveCount            int64  // kbkill_heartbeat_stale: alive VC count at last beat
	QwertyVisible         int64  // kbkill_heartbeat_stale: 1=visible, 2=hidden, 0=unknown
	AppearFpMB            int64  // kbkill_heartbeat_stale: phys_footprint at viewWillAppear-end
	CrashStack            string // kbkill_metrickit_crash: leaf-first binary+offset frames, ≤200 chars, empty when stack unavailable
}

// OnError is called with a structured error event. Nil by default —
// community builds leave it nil. The private gateway main() assigns
// a metrics writer. Implementations must be non-blocking.
var OnError func(ctx context.Context, e ErrorEvent)

// OnRequestFailed marks the in-flight request as failed with a closed-vocabulary
// error type. Nil by default — community builds leave it nil. The private gateway
// main() assigns a hook that flips the request log entry's Success=false and
// sets ErrorType. Callers must invoke this on every error-return path where the
// `requests` measurement would otherwise record success=true.
var OnRequestFailed func(ctx context.Context, errorType string)
