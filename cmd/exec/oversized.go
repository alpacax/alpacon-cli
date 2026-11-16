package exec

import (
	"io"
	"net/http"
	"strings"

	"github.com/alpacax/alpacon-cli/utils"
)

const inlineLineInterpreter = "/bin/sh" // what the agent hands an inline line to, so the file means the same

// isLineTooLong reports whether err is a 400 whose field_errors.line has a max_length entry; the
// same code on another field, or only at the top level, is some other refusal.
func isLineTooLong(err error) bool {
	return utils.HTTPStatusCode(err) == http.StatusBadRequest && utils.HasFieldErrorCode(err, "line", "max_length")
}

// lineTooLongGuidance words a length refusal and the file-lane command to run instead. Nothing is
// written to the server on the user's behalf: an upload outlives a refusal and is not owner-only.
func lineTooLongGuidance(parsed RemoteExecArgs) (message, hint string, action utils.NextAction) {
	flags := runAsFlags(parsed)
	if parsed.Purpose != "" {
		flags = append(flags, "--purpose "+argvQuote(utils.SanitizeTerminalText(parsed.Purpose)))
	}
	if parsed.Detach {
		flags = append(flags, "--detach")
	}
	if parsed.Wait {
		flags = append(flags, "--wait")
	}
	if parsed.WaitApprovalRaw != "" {
		flags = append(flags, "--wait-approval "+argvQuote(parsed.WaitApprovalRaw))
	}
	action = uploadScriptAction(append(flags, "--interpreter "+inlineLineInterpreter), parsed.Server)
	action.Description = "save the command to a local file, then write it to the server and run it in one step with " + inlineLineInterpreter + ", the shell a command line runs in, adding --overwrite to replace a file already at that path"

	message = "the server refused this command line for its length; run it as a verified file"
	lead := "save the command to a local file, then run:"
	if reason := fileLaneObstacle(parsed); reason != "" {
		message = "the server refused this command line for its length, and it cannot run as a verified file as it stands: " + reason
		lead = "once that is fixed, save the command to a local file and run:"
	}
	return message, denialHintLine(lead + "\n  " + action.Command + "\n"), action
}

// fileLaneObstacle says what keeps the file lane from taking the command as it stands: --env,
// content over 64 KB, or content that is not valid UTF-8.
func fileLaneObstacle(parsed RemoteExecArgs) string {
	if len(parsed.Env) > 0 {
		return "--env values cannot ride the file lane; set the variables inside the script"
	}
	_, reason := readScriptContent(strings.NewReader(parsed.Command), "the command line")
	return reason
}

// lineTooLongFailure returns exit 1 with the length refusal and the file-lane command to run instead.
func lineTooLongFailure(stderr io.Writer, err error, parsed RemoteExecArgs) error {
	message, hint, action := lineTooLongGuidance(parsed)
	return codedRefusalWithExit(stderr, utils.ExitCodeGeneralError, "command", err, message, hint, []utils.NextAction{action})
}
