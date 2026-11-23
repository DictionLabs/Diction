package core

type Backend struct {
	Name            string
	URL             string
	Aliases         []string
	CanonicalID     string
	DisplayName     string
	Description     string
	Provider        string
	NeedsWAV        bool
	ForwardModel    string
	AuthHeader      string
	SkipHealthCheck bool
	TargetPath      string
	Disabled        bool

	Languages map[string]bool

	NeedsExplicitLanguage bool

	AcceptsCustomWords bool
}

func langSet(codes map[string]bool) map[string]bool {
	out := make(map[string]bool, len(codes))
	for k, v := range codes {
		out[k] = v
	}
	return out
}

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
		canonical = nameCustom
	}
	return &Backend{
		Name:            nameCustom,
		URL:             rawURL,
		Aliases:         []string{nameCustom},
		CanonicalID:     canonical,
		DisplayName:     "Custom",
		Description:     "custom backend",
		Provider:        nameCustom,
		NeedsWAV:        EnvBoolOrDefault("CUSTOM_BACKEND_NEEDS_WAV", false),
		ForwardModel:    forwardModel,
		AuthHeader:      EnvOrDefault("CUSTOM_BACKEND_AUTH", ""),
		SkipHealthCheck: true,
	}
}

func DefaultBackends() []Backend {
	return append(whisperBackends(), speechBackends()...)
}

func whisperBackends() []Backend {
	return []Backend{
		{
			Name:         "small",
			URL:          "http://whisper-small:8000",
			Aliases:      []string{"small", "Systran/faster-whisper-small", modelWhisperSmallCT2},
			CanonicalID:  modelWhisperSmallCT2,
			ForwardModel: modelWhisperSmallCT2,
			DisplayName:  "Small",
			Description:  "fast, good for everyday dictation",
			Provider:     nameWhisper,
		},
		{
			Name:         "medium",
			URL:          "http://whisper-medium:8000",
			Aliases:      []string{"medium", "Systran/faster-whisper-medium", modelWhisperMediumCT2},
			CanonicalID:  modelWhisperMediumCT2,
			ForwardModel: modelWhisperMediumCT2,
			DisplayName:  "Medium",
			Description:  "slower, handles accents and background noise better",
			Provider:     nameWhisper,
		},
		{
			Name:         "large-v3-turbo",
			URL:          "http://whisper-large-turbo:8000",
			Aliases:      []string{"large-v3-turbo", "turbo", "deepdml/faster-whisper-large-v3-turbo-ct2", "DictionLabs/whisper-large-v3-turbo-ct2"},
			CanonicalID:  "DictionLabs/whisper-large-v3-turbo-ct2",
			ForwardModel: "deepdml/faster-whisper-large-v3-turbo-ct2",
			DisplayName:  "Large",
			Description:  "highest accuracy Whisper model, best for difficult audio",
			Provider:     nameWhisper,
		},
		{
			Name:        "distil-large-v3",
			URL:         "http://whisper-distil-large:8000",
			Aliases:     []string{"distil-large-v3", "Systran/faster-distil-whisper-large-v3"},
			CanonicalID: "Systran/faster-distil-whisper-large-v3",
			DisplayName: "Large",
			Description: "highest accuracy Whisper model, English only",
			Provider:    nameWhisper,
			Languages:   langSet(map[string]bool{"en": true}),
		},
	}
}

func speechBackends() []Backend {
	return []Backend{
		{
			Name: "parakeet-v3",
			URL:  "http://parakeet:5092",
			Aliases: []string{
				"parakeet-v3", nameParakeet, "parakeet-tdt-0.6b-v3", "nvidia/parakeet-tdt-0.6b-v3",
				"parakeet-ultra", "DictionLabs/parakeet-ultra",
			},
			CanonicalID:        "nvidia/parakeet-tdt-0.6b-v3",
			DisplayName:        "Parakeet",
			Description:        "best overall accuracy and speed, 25 European languages",
			Provider:           nameParakeet,
			NeedsWAV:           true,
			Languages:          langSet(euLanguages),
			AcceptsCustomWords: true,
		},

		{
			Name:                  "canary-v2",
			URL:                   "http://canary:9000",
			Aliases:               []string{"canary-v2", nameCanary, "nvidia/canary-1b-v2"},
			CanonicalID:           "nvidia/canary-1b-v2",
			DisplayName:           "Canary",
			Description:           "highest accuracy for 25 European languages, GPU-accelerated",
			Provider:              nameCanary,
			NeedsWAV:              true,
			TargetPath:            "/inference",
			Languages:             langSet(euLanguages),
			NeedsExplicitLanguage: true,
			AcceptsCustomWords:    true,
		},

		{
			Name:                  "canary-qwen",
			URL:                   "http://canary-qwen:9000",
			Aliases:               []string{"canary-qwen", "nvidia/canary-qwen-2.5b"},
			CanonicalID:           "nvidia/canary-qwen-2.5b",
			DisplayName:           "Canary Qwen",
			Description:           "best-in-class English speech recognition, 5.63% WER",
			Provider:              nameCanary,
			NeedsWAV:              true,
			TargetPath:            "/inference",
			Disabled:              true,
			Languages:             langSet(map[string]bool{"en": true}),
			NeedsExplicitLanguage: true,
		},

		{
			Name:               "qwen3-asr",
			URL:                "http://qwen-asr:5096",
			Aliases:            []string{"qwen3-asr", "qwen-asr", "Qwen/Qwen3-ASR-1.7B", "DictionLabs/Qwen3-ASR-1.7B"},
			CanonicalID:        "Qwen/Qwen3-ASR-1.7B",
			DisplayName:        "Qwen3-ASR",
			Description:        "Chinese and Cantonese speech recognition",
			Provider:           "qwen",
			NeedsWAV:           true,
			Languages:          langSet(map[string]bool{"zh": true, langYue: true}),
			AcceptsCustomWords: true,
		},

		{
			Name:               "cohere-transcribe",
			URL:                "http://cohere:5093",
			Aliases:            []string{"cohere-transcribe", nameCohere, "CohereLabs/cohere-transcribe-03-2026"},
			CanonicalID:        "CohereLabs/cohere-transcribe-03-2026",
			DisplayName:        "Cohere Transcribe",
			Description:        "fast multilingual speech recognition, 14 languages",
			Provider:           nameCohere,
			NeedsWAV:           true,
			Languages:          langSet(cohereSupported),
			AcceptsCustomWords: true,
		},
	}
}
