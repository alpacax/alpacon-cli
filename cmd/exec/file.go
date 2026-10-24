package exec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/alpacax/alpacon-cli/api/event"
	ftpapi "github.com/alpacax/alpacon-cli/api/ftp"
	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

// FileContentMaxBytes is the server's ceiling on a verified file's content:
// 64 KB, counted in UTF-8 bytes. Checked locally so an oversized script is
// refused before it travels.
const FileContentMaxBytes = 65536

// DefaultInterpreter runs a verified file when --interpreter names none.
const DefaultInterpreter = "/bin/bash"

const (
	reuseDaysMin = 1
	reuseDaysMax = 366
)

// fileExecRefusals maps the server's file-lane error codes to guidance. Codes
// mirror the server by hand—nothing enforces the sync, so fileExecRefusal
// answers only for codes it carries and leaves the rest to the generic error
// path. The two clientBug entries name fields this CLI never sends on the file
// lane: reaching one means the request builder regressed, not that the user
// did anything wrong.
var fileExecRefusals = []struct {
	code, message, hint string
	// needsServer says message and hint are Sprintf formats taking the server
	// name; the rest are printed as they are.
	needsServer bool
	clientBug   bool
}{
	{
		code:        "file_exec_unsupported_agent",
		message:     "the agent on '%s' does not offer the file lane; it needs Alpamon 2.8.3 or newer on a platform that has the file lane",
		hint:        "if the server runs Alpamon older than 2.8.3, update it to 2.8.3 or newer.\nOn a platform without the file lane, such as Windows, updating does not help.\nEither way, run the script as an ordinary command with an interpreter the server has, for example:\n  alpacon exec %s -- /bin/bash /path/to/script.sh\n",
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
		code:    "file_exec_invalid_reuse_days",
		message: fmt.Sprintf("the server refused the reuse proposal: a reuse duration must be %d to %d days", reuseDaysMin, reuseDaysMax),
		hint:    "propose a duration in that range, or omit --reuse-days to propose none; the approver may then still grant reuse up to the workspace ceiling (1 to 366 days, default 90).\n",
	},
	{
		code:    "file_exec_reuse_exceeds_max",
		message: "the server refused the reuse proposal: this workspace's file execution grant ceiling is shorter than the duration proposed",
		hint:    "resubmit with a shorter --reuse-days, or omit it; the approver may then still grant reuse up to the workspace ceiling (1 to 366 days, default 90).\n",
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
// --file, --file-from, --interpreter, --reuse-days, --upload, --overwrite,
// --from-server and the arguments after --.
// From and Interpreter are as typed, empty when the flag was not given;
// loadFileExecution fills the defaults, so a re-run hint can repeat only what
// the user said.
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
	// ReuseDays is the proposed reuse duration in days; 0 means none was given.
	ReuseDays int
	// Upload writes the content to Path on the server before the submission,
	// so the file need not be there already.
	Upload bool
	// Overwrite lets Upload replace a file already at Path; without it the
	// agent refuses an existing file.
	Overwrite bool
	// FromServer reads the content from Path on the server instead of a local
	// file, for a script that exists only there.
	FromServer bool
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
	content, msg := readFileContent(from, spec.From == "")
	if msg != "" {
		return event.FileExecution{}, msg
	}
	return newFileExecution(spec, content), ""
}

// newFileExecution builds the submission around content, filling the default
// interpreter.
func newFileExecution(spec FileExecArgs, content string) event.FileExecution {
	interpreter := spec.Interpreter
	if interpreter == "" {
		interpreter = DefaultInterpreter
	}
	// Args may be nil here; SubmitFileCommand sends it as the empty list the
	// server defaults to.
	return event.FileExecution{
		Path:        spec.Path,
		Interpreter: interpreter,
		Args:        spec.Args,
		Content:     content,
		ReuseDays:   spec.ReuseDays,
	}
}

// uploadFileOrExit writes the script to its --file path on the server before
// the submission, as the account and group the script will run as. It sends
// file.Content, the bytes the submission carries, so what lands on the server is
// what the reviewer sees; the agent still hashes the file at execution and
// refuses it if a byte changed in between. It exits on failure, since a script
// that was not written must not be submitted.
func uploadFileOrExit(ac *client.AlpaconClient, parsed RemoteExecArgs, file event.FileExecution, workSessionID, authMethod string) {
	upload := func() error {
		return ftpapi.UploadContentAs(ac, parsed.Server, file.Path, []byte(file.Content),
			parsed.Username, parsed.Groupname, workSessionID, parsed.File.Overwrite)
	}
	err := upload()
	if err != nil {
		err = utils.HandleCommonErrors(err, parsed.Server, mfa.ErrorCallbacks(ac, upload))
	}
	if err == nil {
		return
	}
	utils.HandleWorkSessionError(err, utils.OperationWebFTP, parsed.Server, authMethod, workSessionID)
	var hint string
	// The agent answers "PATH already exists." for an existing file.
	if !parsed.File.Overwrite && strings.Contains(err.Error(), "already exists") {
		hint = denialHintLine("pass --overwrite to replace the file on the server, or use --from-server instead of --upload (and drop --file-from) to submit the copy already there.\n")
	}
	// reportCodedRefusal writes raw and appends its own period; drop the one the
	// agent's message ends with.
	msg := strings.TrimSuffix(utils.SanitizeTerminalText(fmt.Sprintf("failed to upload the script to '%s:%s': %s", parsed.Server, file.Path, err)), ".")
	reportCodedRefusal(utils.OperationWebFTP, err, msg, hint)
}

// downloadFileOrExit reads the script from its --file path on the server, as
// the account and group it will run as, and builds the submission from those
// bytes. The requester still submits the content the reviewer judges, and the
// agent still hashes the file at execution and refuses it if a byte changed in
// between. It exits on failure, since a script that could not be read must not
// be submitted.
func downloadFileOrExit(ac *client.AlpaconClient, parsed RemoteExecArgs, workSessionID, authMethod string) event.FileExecution {
	label := parsed.Server + ":" + parsed.File.Path
	content, msg, err := fetchServerScript(ac, parsed, label, workSessionID)
	if err != nil {
		utils.HandleWorkSessionError(err, utils.OperationWebFTP, parsed.Server, authMethod, workSessionID)
		// reportCodedRefusal writes raw and appends its own period; drop one the
		// agent's message ends with.
		msg = strings.TrimSuffix(utils.SanitizeTerminalText(fmt.Sprintf("cannot read the script from '%s' on the server: %s", label, err)), ".")
		reportCodedRefusal(utils.OperationWebFTP, err, msg, "")
		return event.FileExecution{}
	}
	if msg != "" {
		utils.CliErrorWithExit("%s", msg)
		return event.FileExecution{}
	}
	return newFileExecution(*parsed.File, content)
}

// fetchServerScript reads the --file path from the server into memory, at
// most FileContentMaxBytes+1 bytes of it, and checks it as readFileContent
// checks a local script. err is a failed download; msg is a copy the file lane
// cannot take.
func fetchServerScript(ac *client.AlpaconClient, parsed RemoteExecArgs, label, workSessionID string) (content, msg string, err error) {
	var data []byte
	download := func() (err error) {
		data, err = ftpapi.DownloadFileContent(ac, parsed.Server, parsed.File.Path,
			parsed.Username, parsed.Groupname, workSessionID, FileContentMaxBytes+1)
		return err
	}
	if err = download(); err != nil {
		err = utils.HandleCommonErrors(err, parsed.Server, mfa.ErrorCallbacks(ac, download))
	}
	if err != nil {
		return "", "", err
	}
	content, msg = readScriptContent(bytes.NewReader(data), label)
	return content, msg, nil
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
	return readScriptContent(f, path)
}

// readScriptContent reads at most FileContentMaxBytes+1 bytes from r and checks
// them against what the file lane accepts. label names the script in the
// message.
func readScriptContent(r io.Reader, label string) (string, string) {
	data, err := io.ReadAll(io.LimitReader(r, FileContentMaxBytes+1))
	if err != nil {
		return "", fmt.Sprintf("cannot read the script from '%s': %s", label, describeOpenError(err))
	}
	switch {
	case len(data) == 0:
		return "", fmt.Sprintf("'%s' is empty; a verified file needs content to hash", label)
	case len(data) > FileContentMaxBytes:
		return "", fmt.Sprintf("'%s' exceeds %d bytes (64 KB), the ceiling for a verified file", label, FileContentMaxBytes)
	case !utf8.Valid(data):
		return "", fmt.Sprintf("'%s' is not valid UTF-8; the content travels as JSON text, which would alter the bytes the agent hashes", label)
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
			server := utils.SanitizeTerminalText(serverName) // reportCodedRefusal writes raw
			message = fmt.Sprintf(message, server)
			if hint != "" {
				hint = fmt.Sprintf(hint, server)
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

const fileExecInlineCredentialMessage = "server rejected this script—it carries a credential"

// fileExecInlineCredentialHint replaces credentialInlineHint on the file lane.
// --env is refused alongside --file, and an ordinary command line would run the
// script unreviewed, so it names neither.
const fileExecInlineCredentialHint = "take the secret out of the script and have the script read it from a file or the environment on the host at run time, so it is not stored with the reviewed script.\n"

func fileExecInlineCredentialRefusal(err error) (message, hint string, ok bool) {
	if !isCommandInlineCredentialError(err) {
		return "", "", false
	}
	return fileExecInlineCredentialMessage, denialHintLine(fileExecInlineCredentialHint), true
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

// HandleFileExecInlineCredential reports command_inline_credential with the
// file lane's hint and exits 1, or returns false when err is something else.
// Call it only on the file lane: the shell lane shares the code and answers it
// in HandleCommandResult with --env.
func HandleFileExecInlineCredential(err error) bool {
	message, hint, ok := fileExecInlineCredentialRefusal(err)
	if !ok {
		return false
	}
	reportCodedRefusal("command", err, message, hint)
	return true
}

// useFileExecutionRefusal renders a command the gate refused with
// rejection_cause use_file_execution as a message and the commands that replace
// it on the file lane. The script was on the command line, so there is no path
// to name and the commands carry placeholders. --file reads the local copy at
// its path unless --file-from or --from-server says otherwise, so the first
// command writes a local copy to the server in the same run and the second runs
// a copy already on the server. Both repeat the account flags of the refused
// run, since the file lane has to run on the same server as the same account.
// The server token is quoted with argvQuote so the commands stay one argument
// per value when pasted into a shell. withEnv says the refused run carried
// --env, which the file lane cannot pass, so the message warns that the script
// has to read those values on the host. accountNote, when set, follows the
// message: exec logs uses it to say the account flags are placeholders.
func useFileExecutionRefusal(commandID, server string, accountFlags []string, withEnv bool, accountNote string) (message string, actions []utils.NextAction) {
	server = utils.SanitizeTerminalText(server) // reportUseFileExecution writes raw
	if server == "" {
		server = "SERVER"
	}
	message = fmt.Sprintf("the server refused command %s on '%s': its command line carries a script body, which runs only as a verified file (%s)",
		utils.SanitizeTerminalText(commandID), server, event.RejectionCauseUseFileExecution)
	if withEnv {
		message += ". --env values cannot ride the file lane, so the script must read them on the host"
	}
	if accountNote != "" {
		message += ". " + accountNote
	}
	prefix := strings.Join(append([]string{string(ExecInvocation)}, accountFlags...), " ")
	serverArg := argvQuote(server)
	actions = []utils.NextAction{
		{
			Command:     prefix + " --upload --file /path/to/script.sh --file-from ./script.sh " + serverArg,
			Description: "save the script to a local file, then write it to the server and run it in one step, adding --overwrite to replace a file already at that path",
		},
		{
			Command:     prefix + " --file /path/to/script.sh --from-server " + serverArg,
			Description: "or, when a copy is already saved at that path on the server, run it there",
		},
	}
	return message, actions
}

// useFileExecutionAccountFlags returns the -u, -g and --work-session flags of
// the refused run, sanitized for a raw write and quoted with argvQuote so a
// pasted command keeps each value one argument, in the order reRunHint uses.
func useFileExecutionAccountFlags(parsed RemoteExecArgs) []string {
	quote := func(s string) string { return argvQuote(utils.SanitizeTerminalText(s)) }
	var flags []string
	if parsed.Username != "" {
		flags = append(flags, "-u "+quote(parsed.Username))
	}
	if parsed.Groupname != "" {
		flags = append(flags, "-g "+quote(parsed.Groupname))
	}
	if parsed.WorkSessionID != "" {
		flags = append(flags, "--work-session "+quote(parsed.WorkSessionID))
	}
	return flags
}

// HandleUseFileExecutionRefusal reports a use_file_execution refusal with the
// file lane guidance and exits 6, or returns false when err is something else.
// It runs ahead of HandleCommandResult, which knows neither the server nor the
// account flags the guidance repeats.
func HandleUseFileExecutionRefusal(err error, parsed RemoteExecArgs) bool {
	var rejected *event.CommandRejectedError
	if !errors.As(err, &rejected) || rejected.Cause != event.RejectionCauseUseFileExecution {
		return false
	}
	reportUseFileExecution(rejected, parsed.Server, useFileExecutionAccountFlags(parsed), len(parsed.Env) > 0, "")
	return true
}

// reportUseFileExecution prints the use_file_execution refusal and exits 6, the
// code every settled-without-a-grant command exits with: running the same line
// again is refused the same way. Under --output json the envelope carries the
// code as error_code and the replacement commands as next_actions.
func reportUseFileExecution(rejected *event.CommandRejectedError, server string, accountFlags []string, withEnv bool, accountNote string) {
	message, actions := useFileExecutionRefusal(rejected.CommandID, server, accountFlags, withEnv, accountNote)
	if utils.OutputFormat == utils.OutputFormatJSON {
		utils.CliErrorEnvelopeWithNextActions(utils.ExitCodeNotApproved, "command", rejected, message+".", actions)
		return
	}
	fmt.Fprintf(os.Stderr, "%s: %s.\n", utils.Red("Error"), message)
	for i, action := range actions {
		line := action.Description + ":\n  " + action.Command + "\n"
		if i == 0 {
			line = denialHintLine(line)
		}
		fmt.Fprint(os.Stderr, line)
	}
	os.Exit(utils.ExitCodeNotApproved)
}
