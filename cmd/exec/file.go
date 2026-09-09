package exec

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/utils"
)

// FileContentMaxBytes is the server's ceiling on a verified file's content
// (ADR 0053): 64 KB, counted in UTF-8 bytes. Checked locally so an oversized
// script is refused before it travels.
const FileContentMaxBytes = 65536

// DefaultInterpreter runs a verified file when --interpreter names none.
const DefaultInterpreter = "/bin/bash"

// fileExecRefusals maps the server's file-lane error codes (alpacon-server
// utils/error_codes.py, ADR 0053) to guidance. Codes mirror the server by
// hand—nothing enforces the sync, so fileExecRefusal answers only for codes it
// carries and leaves the rest to the generic error path. The two clientBug
// entries name fields this CLI never sends on the file lane: reaching one means
// the request builder regressed, not that the user did anything wrong.
var fileExecRefusals = []struct {
	code, message, hint string
	// needsServer says message and hint are Sprintf formats taking the server
	// name; the rest are printed as they are.
	needsServer bool
	clientBug   bool
}{
	{
		code:        "file_exec_unsupported_agent",
		message:     "the agent on '%s' cannot verify a file digest; Alpamon 2.6.0 or newer is required",
		hint:        "update Alpamon on the server, or run the script as an ordinary command until then:\n  alpacon exec %s -- /bin/bash /path/to/script.sh\n",
		needsServer: true,
	},
	{
		code:        "file_exec_assessor_disabled",
		message:     "this deployment has the command assessor disabled, so '%s' cannot run a verified file",
		hint:        "run the script as an ordinary command instead:\n  alpacon exec %s -- /bin/bash /path/to/script.sh\n",
		needsServer: true,
	},
	{
		code:    "file_exec_invalid_path",
		message: "the server refused the file path or the interpreter: both must be absolute paths starting with /",
	},
	{
		code:    "file_exec_content_too_large",
		message: fmt.Sprintf("the server refused the file: a verified file is limited to %d bytes (64 KB)", FileContentMaxBytes),
		hint:    "split the script, or move the bulk of it into a file the script reads at run time.\n",
	},
	{
		code:    "file_exec_empty_content",
		message: "the server refused the file: it is empty, and a verified file needs content to hash",
	},
	{
		code:    "file_exec_line_too_long",
		message: "the interpreter, the path and the arguments together exceed the server's command line ceiling",
		hint:    "shorten the arguments, or read them from a file inside the script.\n",
	},
	{
		code:    "file_exec_env_not_allowed",
		message: "the server refused the request: environment variables are not allowed on the file lane",
		hint:    "set the variables inside the script, where they are reviewed and hashed with it.\n",
	},
	{
		code:      "file_exec_line_not_allowed",
		message:   "the server refused the request: it carried a command line alongside the file",
		clientBug: true,
	},
	{
		code:      "file_exec_data_not_allowed",
		message:   "the server refused the request: it carried a data field alongside the file",
		clientBug: true,
	},
}

// FileExecArgs is the file lane as the user asked for it on the command line:
// --file, --file-from, --interpreter and the arguments after --. From and
// Interpreter are as typed, empty when the flag was not given; loadFileExecution
// fills the defaults, so a re-run hint can repeat only what the user said.
type FileExecArgs struct {
	// Path is the script's location on the target server, and by default the
	// local file the content is read from.
	Path string
	// From, when set, is the local file the content is read from instead.
	From string
	// Interpreter runs the file on the target; DefaultInterpreter when empty.
	Interpreter string
	// Args are passed to the script as given, one argv entry each.
	Args []string
}

// loadFileExecution reads the script's bytes and builds the submission, or
// returns the message to refuse with. The bytes are sent exactly as read—no
// trimming, no newline normalization—because the agent hashes the file on the
// target's disk and one changed byte fails the run closed. The content travels
// as JSON text, so it has to be valid UTF-8: encoding/json would replace an
// invalid sequence with U+FFFD and the digest would never match.
func loadFileExecution(spec FileExecArgs) (event.FileExecution, string) {
	from := spec.From
	if from == "" {
		from = spec.Path
	}
	interpreter := spec.Interpreter
	if interpreter == "" {
		interpreter = DefaultInterpreter
	}

	content, msg := readFileContent(from, spec.From == "")
	if msg != "" {
		return event.FileExecution{}, msg
	}

	// Args may be nil here; SubmitFileCommand sends it as the empty list the
	// server defaults to.
	return event.FileExecution{
		Path:        spec.Path,
		Interpreter: interpreter,
		Args:        spec.Args,
		Content:     content,
	}, ""
}

// readFileContent reads at most FileContentMaxBytes+1 bytes of the file at
// path, so a file far over the ceiling is refused without being read whole.
// defaultedPath says the local path was taken from --file, which is what makes
// --file-from the fix worth naming when the read fails.
func readFileContent(path string, defaultedPath bool) (string, string) {
	f, err := os.Open(path)
	if err != nil {
		msg := fmt.Sprintf("cannot read the script from '%s': %s", path, describeOpenError(err))
		if defaultedPath {
			msg += "; the content is read from the same path locally, pass --file-from LOCAL_PATH to read it from elsewhere"
		}
		return "", msg
	}
	defer func() { _ = f.Close() }()

	data, err := io.ReadAll(io.LimitReader(f, FileContentMaxBytes+1))
	if err != nil {
		return "", fmt.Sprintf("cannot read the script from '%s': %s", path, describeOpenError(err))
	}
	switch {
	case len(data) == 0:
		return "", fmt.Sprintf("'%s' is empty; a verified file needs content to hash", path)
	case len(data) > FileContentMaxBytes:
		return "", fmt.Sprintf("'%s' exceeds %d bytes (64 KB), the ceiling for a verified file", path, FileContentMaxBytes)
	case !utf8.Valid(data):
		return "", fmt.Sprintf("'%s' is not valid UTF-8; the content travels as JSON text, which would alter the bytes the agent hashes", path)
	}
	return string(data), ""
}

// argvQuote quotes one file-lane value for the re-run hint. These values were
// argv on the first run and reached the server as a JSON list, so no shell ever
// interpreted them; shellQuote quotes only on whitespace because the generic
// lane wants metacharacters to reach the remote shell, and that would let `a;b`
// or `$(...)` execute locally when the hint is pasted. Anything outside a
// conservative POSIX-safe set is single-quoted.
func argvQuote(s string) string {
	if s == "" {
		return "''"
	}
	for _, r := range s {
		if !isArgvSafe(r) {
			return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
		}
	}
	return s
}

func isArgvSafe(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return strings.ContainsRune("_./:=@%+,-", r)
}

// describeOpenError strips the "open <path>:" prefix os.Open puts on its error,
// since the message already names the path once.
func describeOpenError(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err.Error()
	}
	return err.Error()
}

// fileExecRefusal renders a file-lane refusal from the server as a message and
// an optional hint, both without a trailing newline on the message and with one
// on the hint, matching the credential and sudo hints. serverName fills the
// placeholders. It answers false for every other error, so the caller falls
// through to the generic path.
func fileExecRefusal(err error, serverName string) (message, hint string, ok bool) {
	if err == nil {
		return "", "", false
	}
	code, _ := utils.ParseErrorResponse(err)
	if code == "" {
		return "", "", false
	}
	for _, r := range fileExecRefusals {
		if r.code != code {
			continue
		}
		message, hint = r.message, r.hint
		if r.needsServer {
			message = fmt.Sprintf(message, serverName)
			if hint != "" {
				hint = fmt.Sprintf(hint, serverName)
			}
		}
		switch {
		case r.clientBug:
			hint = denialHintLine(fmt.Sprintf(
				"this is a bug in alpacon-cli, not in your request (%s). Please report it at https://github.com/alpacax/alpacon-cli/issues\n", code))
		case hint != "":
			hint = denialHintLine(hint)
		}
		return message, hint, true
	}
	return "", "", false
}

// HandleFileExecRefusal reports a file-lane refusal and exits 1, or returns
// false when err is something else. Under --output json the envelope carries the
// server's code; table mode prints the message and the hint. It runs before
// HandleCommandResult, which knows no server name and would print the raw code.
func HandleFileExecRefusal(err error, serverName string) bool {
	message, hint, ok := fileExecRefusal(err, serverName)
	if !ok {
		return false
	}
	reportCodedRefusal("command", err, message, hint)
	return true
}
