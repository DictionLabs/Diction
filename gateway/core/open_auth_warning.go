package core

import "log"

// PairingState tells the warning whether its "pair the app" advice can work.
type PairingState int

const (
	// PairingNone is a build without pairing (the private cloud build).
	PairingNone PairingState = iota
	// PairingReady means the pairing key loaded, so 'gateway auth' works.
	PairingReady
	// PairingDisabled means the pairing key could not be created or read, so no key can be issued.
	PairingDisabled
)

const openAuthBase = "WARNING: this gateway accepts unauthenticated transcription requests ("
const openAuthAnyone = "Anyone who can reach this port can use your speech backends. "

// OpenAuthWarning is the startup line for mode optional with pairing ready.
const OpenAuthWarning = openAuthBase +
	"DICTION_GATEWAY_AUTH=optional is the default, so requests without a key are served). " + openAuthAnyone +
	"To require a key set DICTION_GATEWAY_AUTH=required and pair the app with 'docker exec <container> gateway auth'."

// OpenAuthWarningLine returns the warning for mode optional or off, or "" when a key is required.
func OpenAuthWarningLine(mode string, pairing PairingState) string {
	var why string
	switch mode {
	case "optional":
		if pairing == PairingReady {
			return OpenAuthWarning
		}
		why = "DICTION_GATEWAY_AUTH=optional, so requests without a key are served). "
	case "off":
		why = "DICTION_GATEWAY_AUTH=off, so requests without a key are served and no key is checked). "
	default:
		return ""
	}
	var fix string
	switch pairing {
	case PairingReady:
		fix = "To require a key set DICTION_GATEWAY_AUTH=required and pair the app with 'docker exec <container> gateway auth'."
	case PairingDisabled:
		fix = "Pairing is disabled because the pairing key could not be written: set DICTION_KEY_PATH to a writable path " +
			"first, then set DICTION_GATEWAY_AUTH=required to require a key. Until then keep this port off untrusted networks."
	default:
		fix = "Keep this port off untrusted networks."
	}
	return openAuthBase + why + openAuthAnyone + fix
}

// LogOpenAuthWarning logs the warning for mode "optional" or "off"; auth behaviour is unchanged.
func LogOpenAuthWarning(mode string, pairing PairingState) bool {
	line := OpenAuthWarningLine(mode, pairing)
	if line == "" {
		return false
	}
	log.Print(line)
	return true
}
