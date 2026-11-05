package exec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/utils"
)

// fileLaneRefusal says why an oversized command line cannot take the file lane and the command to
// run instead.
type fileLaneRefusal struct {
	message string
	action  utils.NextAction
}

// isLineTooLong reports whether err is the server's 400 for a command line over its length limit:
// field_errors.line carries an entry coded max_length. A max_length on another field, or only on
// the top-level code, is some other refusal.
func isLineTooLong(err error) bool {
	return utils.HTTPStatusCode(err) == http.StatusBadRequest && utils.HasFieldErrorCode(err, "line", "max_length")
}

// commandFilePath derives the server path an oversized command is written to from its content, so
// the same command lands on the same path and a standing approval of it is reused.
func commandFilePath(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "/tmp/.alpacon-exec-" + hex.EncodeToString(sum[:])[:16] + ".sh"
}

func oversizedNotice(file event.FileExecution) string {
	return fmt.Sprintf("Command line exceeds the server's limit; running it as a verified file at %s with %s (the file stays on the server)", file.Path, file.Interpreter)
}

// fileLaneForCommand turns an inline command the server refused for length into file-lane
// arguments at a content-derived path, or answers why the file lane cannot take it: --env, content
// over the 64 KB ceiling, or content that is not valid UTF-8.
func fileLaneForCommand(parsed RemoteExecArgs) (RemoteExecArgs, *fileLaneRefusal) {
	var reason string
	if len(parsed.Env) > 0 {
		reason = "--env values cannot ride the file lane; set the variables inside the script"
	} else if _, msg := readScriptContent(strings.NewReader(parsed.Command), "the command line"); msg != "" {
		reason = msg
	}
	if reason != "" {
		flags := runAsFlags(parsed)
		if parsed.Purpose != "" {
			flags = append(flags, "--purpose "+argvQuote(utils.SanitizeTerminalText(parsed.Purpose)))
		}
		if parsed.Detach {
			flags = append(flags, "--detach")
		}
		if parsed.ReuseDays > 0 {
			flags = append(flags, "--reuse-days "+strconv.Itoa(parsed.ReuseDays))
		}
		return RemoteExecArgs{}, &fileLaneRefusal{
			message: "the server refused this command line for its length, and it cannot run as a verified file: " + reason,
			action:  uploadScriptAction(flags, parsed.Server),
		}
	}
	switched := parsed
	switched.Command = ""
	switched.ReuseDays = 0
	switched.File = &FileExecArgs{
		Path:        commandFilePath(parsed.Command),
		Upload:      true,
		Overwrite:   true,
		ReuseDays:   parsed.ReuseDays,
		FromCommand: true,
	}
	return switched, nil
}
