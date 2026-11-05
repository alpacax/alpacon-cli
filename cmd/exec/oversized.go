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

// fileLaneRefusal holds why an oversized command line cannot take the file lane and the command to
// run instead.
type fileLaneRefusal struct {
	message string
	action  utils.NextAction
}

// isLineTooLong reports whether err is a 400 whose field_errors.line has a max_length entry; the
// same code on another field, or only at the top level, is some other refusal.
func isLineTooLong(err error) bool {
	return utils.HTTPStatusCode(err) == http.StatusBadRequest && utils.HasFieldErrorCode(err, "line", "max_length")
}

// commandFilePath hashes the run-as account with the content, so a rerun reuses one file and its
// approval, and another account never meets a file under sticky /tmp it cannot overwrite.
func commandFilePath(username, content string) string {
	sum := sha256.Sum256([]byte(username + "\x00" + content))
	return "/tmp/.alpacon-exec-" + hex.EncodeToString(sum[:])[:16] + ".sh"
}

func oversizedNotice(file event.FileExecution) string {
	return fmt.Sprintf("Command line exceeds the server's limit; running it as a verified file at %s with %s (the file stays on the server)", file.Path, file.Interpreter)
}

// fileLaneForCommand turns a command refused for length into file-lane arguments, or says why the
// file lane cannot take it: --env, content over 64 KB, or content that is not valid UTF-8.
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
		Path:        commandFilePath(parsed.Username, parsed.Command),
		Upload:      true,
		Overwrite:   true,
		ReuseDays:   parsed.ReuseDays,
		FromCommand: true,
	}
	return switched, nil
}
