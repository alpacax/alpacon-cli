package exec

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// inlineCommandLimit is the maximum command size (in bytes) sent inline to the
// server. Larger commands are routed through the upload-then-execute bypass.
// Kept in sync with alpacon-server Command.line CharField(max_length=2048).
const inlineCommandLimit = 2048

// tempScriptDir is the remote directory where oversized command scripts are
// staged before execution.
const tempScriptDir = "/tmp"

// exceedsInlineLimit reports whether command is too large to send inline.
// Byte-based (len), a strict subset of the server's char-based max_length: the
// CLI only ever bypasses early, never late.
func exceedsInlineLimit(command string) bool {
	return len(command) > inlineCommandLimit
}

// tempScriptName returns the remote filename for an oversized command script.
func tempScriptName(id string) string {
	return fmt.Sprintf(".alpacon-exec-%s.sh", id)
}

// tempScriptPath returns the absolute remote path for an oversized command script.
func tempScriptPath(id string) string {
	return tempScriptDir + "/" + tempScriptName(id)
}

// wrapScriptCommand builds the inline wrapper that runs the uploaded script,
// preserves its exit code, and removes the temp file even on non-zero exit.
func wrapScriptCommand(path string) string {
	return fmt.Sprintf("sh %s; rc=$?; rm -f %s; exit $rc", path, path)
}

// isWindowsPlatform reports whether the resolved server platform is Windows.
// Only an explicit windows value is rejected; empty/unknown platforms are
// treated as POSIX and proceed.
func isWindowsPlatform(platform string) bool {
	return strings.EqualFold(strings.TrimSpace(platform), "windows")
}

// newExecID returns a short random hex identifier for a temp script path,
// avoiding collisions between concurrent oversized executions.
func newExecID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// rand.Read failing is effectively impossible; fall back to a fixed
		// token so the upload still proceeds (allow_overwrite covers reuse).
		return "fallback00000000"
	}
	return hex.EncodeToString(b)
}
