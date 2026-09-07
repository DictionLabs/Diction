package main

// modeForIntent maps a request's `?intent=` to the `mode` the gateway reports back.
//
// One definition on purpose. Duplication is what caused the v13 bug this file exists to fix:
// handleTextProcess derived the mode inline while the WebSocket wiring in buildMux returned a
// hardcoded "", so an edit over the audio socket came back tagged as a dictation and the app —
// whose edit branch requires mode == "edit" || "edit-selected" — inserted the LLM's answer as
// text instead of applying it as an edit.
//
// Anything that is not an explicit edit intent is a dictation. The gateway never guesses
// transcribe-vs-edit from what was said; only the client's explicit intent decides.
func modeForIntent(intent string) string {
	switch intent {
	case "edit", "edit-selected":
		return intent
	default:
		return "transcribe"
	}
}
