package event

import "errors"

// inlineCommandLimit matches alpacon-server Command.line max_length=2048.
const inlineCommandLimit = 2048

// resolveOversized reports whether command must be staged as a temp script
// (byte-based; may flag before the server's char limit, never after). Windows
// has no sh wrapper, so an oversized command there is an error.
func resolveOversized(command, platform string) (bool, error) {
	if len(command) <= inlineCommandLimit {
		return false, nil
	}
	if platform == "windows" {
		return false, errors.New("oversized commands are not supported on Windows servers")
	}
	return true, nil
}
