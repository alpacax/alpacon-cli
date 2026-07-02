package event

import "errors"

// inlineCommandLimit matches alpacon-server Command.line max_length=2048.
const inlineCommandLimit = 2048

// resolveOversized reports whether command must be staged as a temp script; byte-based (trips at or before the server's char limit) and errors on hosts the server won't stage on: Windows and unknown (empty) platform are not confirmed sh hosts for the wrapper.
func resolveOversized(command, platform string) (bool, error) {
	if len(command) <= inlineCommandLimit {
		return false, nil
	}
	if platform == "windows" || platform == "" {
		return false, errors.New("oversized commands require a confirmed sh host; this server's platform is Windows or not yet known")
	}
	return true, nil
}
