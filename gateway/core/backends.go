package core

// Backend describes a speech-to-text model backend.
type Backend struct {
	Name            string
	URL             string
	Aliases         []string
	CanonicalID     string // HuggingFace-style ID advertised in /v1/models data[]
	DisplayName     string
	Description     string
	Provider        string // "whisper", "parakeet", or "canary"
	NeedsWAV        bool   // if true, gateway converts audio to WAV before proxying
	ForwardModel    string // model name to inject into forwarded request; empty = don't inject
	AuthHeader      string // Authorization header value to inject into backend requests; empty = none
	SkipHealthCheck bool   // if true, skip health polling (custom/external backends)
	TargetPath      string // HTTP path for the transcription endpoint; empty defaults to /v1/audio/transcriptions
	Disabled        bool   // if true, backend is registered (aliases/models reference) but not deployed — skip warmup + health polling
}

// CustomBackendFromEnv builds a custom backend from environment variables.
// Returns nil if CUSTOM_BACKEND_URL is not set.
//
// Supported env vars:
//
//	CUSTOM_BACKEND_URL          (required) base URL of the backend, e.g. http://192.168.1.50:8000
//	CUSTOM_BACKEND_MODEL        model name to forward in the request, e.g. Systran/faster-whisper-large-v3-turbo
//	CUSTOM_BACKEND_NEEDS_WAV    set to "true" if the backend only accepts WAV audio (default: false)
//	CUSTOM_BACKEND_AUTH         Authorization header value, e.g. "Bearer sk-xxx" (default: none)
//	CUSTOM_BACKEND_CANONICAL_ID HuggingFace-style ID advertised via /v1/models (default: CUSTOM_BACKEND_MODEL, else "custom")
func CustomBackendFromEnv() *Backend {
	rawURL := EnvOrDefault("CUSTOM_BACKEND_URL", "")
	if rawURL == "" {
		return nil
	}
	forwardModel := EnvOrDefault("CUSTOM_BACKEND_MODEL", "")
	canonical := EnvOrDefault("CUSTOM_BACKEND_CANONICAL_ID", "")
	if canonical == "" {
		canonical = forwardModel
	}
	if canonical == "" {
		canonical = "custom"
	}
	return &Backend{
		Name:            "custom",
		URL:             rawURL,
		Aliases:         []string{"custom"},
		CanonicalID:     canonical,
		DisplayName:     "Custom",
		Description:     "custom backend",
		Provider:        "custom",
		NeedsWAV:        EnvBoolOrDefault("CUSTOM_BACKEND_NEEDS_WAV", false),
		ForwardModel:    forwardModel,
		AuthHeader:      EnvOrDefault("CUSTOM_BACKEND_AUTH", ""),
		SkipHealthCheck: true,
	}
}

// DefaultBackends returns the standard set of STT backends.
func DefaultBackends() []Backend {
	return []Backend{
		// Whisper models (faster-whisper-server, accepts any audio format)
		// Tiny is reserved for on-device inference, not exposed via gateway
		// {Name: "tiny", URL: "http://whisper-tiny:8000", Aliases: []string{"tiny", "Systran/faster-whisper-tiny"}, DisplayName: "Tiny", Description: "fastest, best for quick notes in quiet environments", Provider: "whisper"},
		{Name: "small", URL: "http://whisper-small:8000", Aliases: []string{"small", "Systran/faster-whisper-small"}, CanonicalID: "Systran/faster-whisper-small", DisplayName: "Small", Description: "fast, good for everyday dictation", Provider: "whisper"},
		{Name: "medium", URL: "http://whisper-medium:8000", Aliases: []string{"medium", "Systran/faster-whisper-medium"}, CanonicalID: "Systran/faster-whisper-medium", DisplayName: "Medium", Description: "slower, handles accents and background noise better", Provider: "whisper"},
		{Name: "large-v3-turbo", URL: "http://whisper-large-turbo:8000", Aliases: []string{"large-v3-turbo", "turbo", "deepdml/faster-whisper-large-v3-turbo-ct2"}, CanonicalID: "deepdml/faster-whisper-large-v3-turbo-ct2", ForwardModel: "deepdml/faster-whisper-large-v3-turbo-ct2", DisplayName: "Large", Description: "highest accuracy Whisper model, best for difficult audio", Provider: "whisper"},
		{Name: "distil-large-v3", URL: "http://whisper-distil-large:8000", Aliases: []string{"distil-large-v3", "Systran/faster-distil-whisper-large-v3"}, CanonicalID: "Systran/faster-distil-whisper-large-v3", DisplayName: "Large", Description: "highest accuracy Whisper model, English only", Provider: "whisper"},

		// Parakeet (NVIDIA, OpenAI-compatible API, WAV only) — available for public/community gateway self-hosters
		{Name: "parakeet-v3", URL: "http://parakeet:5092", Aliases: []string{"parakeet-v3", "parakeet", "parakeet-tdt-0.6b-v3", "nvidia/parakeet-tdt-0.6b-v3"}, CanonicalID: "nvidia/parakeet-tdt-0.6b-v3", DisplayName: "Parakeet", Description: "best overall accuracy and speed, 25 European languages", Provider: "parakeet", NeedsWAV: true},

		// Canary (NVIDIA, custom /inference API, WAV only, GPU-accelerated) — default for private cloud gateway
		{Name: "canary-v2", URL: "http://canary:9000", Aliases: []string{"canary-v2", "canary", "nvidia/canary-1b-v2"}, CanonicalID: "nvidia/canary-1b-v2", DisplayName: "Canary", Description: "highest accuracy for 25 European languages, GPU-accelerated", Provider: "canary", NeedsWAV: true, TargetPath: "/inference"},

		// Canary-Qwen (NVIDIA, English-only, SALM architecture, #1 HF ASR leaderboard) — English tier for private cloud gateway.
		// Disabled: 9.86 GiB VRAM doesn't fit alongside canary-v2 + whisper on the 16GB GPU (see server/docker-compose.yml).
		// English routing uses canary-v2; this entry stays for alias/models reference but is skipped for warmup + health polling.
		{Name: "canary-qwen", URL: "http://canary-qwen:9000", Aliases: []string{"canary-qwen", "nvidia/canary-qwen-2.5b"}, CanonicalID: "nvidia/canary-qwen-2.5b", DisplayName: "Canary Qwen", Description: "best-in-class English speech recognition, 5.63% WER", Provider: "canary", NeedsWAV: true, TargetPath: "/inference", Disabled: true},

		// Cohere Transcribe (CohereLabs, OpenAI-compatible /v1/audio/transcriptions, WAV only,
		// GPU-accelerated) — primary model for en/es/de/nl/fr/pt/it on the private cloud gateway,
		// per the measured eval (COHERE_TRANSCRIBE_EVAL_RESULTS.md, 2026-07-06). Empty TargetPath
		// (defaults to /v1/audio/transcriptions) matches its native contract exactly, same posture
		// as parakeet-v3 — no response normalization/adapter needed. NOT Disabled: it must be
		// health-polled to be routable by ModelForLanguage/ModelForAutoDetect.
		{Name: "cohere-transcribe", URL: "http://cohere:5093", Aliases: []string{"cohere-transcribe", "cohere", "CohereLabs/cohere-transcribe-03-2026"}, CanonicalID: "CohereLabs/cohere-transcribe-03-2026", DisplayName: "Cohere Transcribe", Description: "fastest + most accurate for English/Spanish, ties Canary on other EU languages", Provider: "cohere", NeedsWAV: true},
	}
}
