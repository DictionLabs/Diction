package main

// modeForIntent maps ?intent= to the reported mode; only an explicit edit intent is an edit.
func modeForIntent(intent string) string {
	switch intent {
	case editIntent, editSelectedIntent:
		return intent
	default:
		return "transcribe"
	}
}
