package core

import "log"

// OpenAuthWarning is the startup line for a gateway that serves keyless requests (mode optional).
const OpenAuthWarning = "WARNING: this gateway accepts unauthenticated transcription requests " +
	"(DICTION_GATEWAY_AUTH=optional is the default, so requests without a key are served). " +
	"Anyone who can reach this port can use your speech backends. " +
	"To require a key set DICTION_GATEWAY_AUTH=required and pair the app with 'docker exec <container> gateway auth'."

// LogOpenAuthWarning logs OpenAuthWarning when mode is "optional"; auth behaviour is unchanged.
func LogOpenAuthWarning(mode string) bool {
	if mode != "optional" {
		return false
	}
	log.Print(OpenAuthWarning)
	return true
}
