package utils

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

const (
	gitIssueURL = "https://github.com/alpacax/alpacon-cli/issues"

	// DebugEnvVar turns on diagnostic output that would be noise in normal use.
	// Any value other than "0" or "false" enables it, matching how the other
	// ALPACON_* switches are read.
	DebugEnvVar = "ALPACON_DEBUG"
)

func reportCLIError() {
	_, _ = fmt.Fprintln(TrackedStderr(), "For issues, check the latest version or report on", gitIssueURL)
}

// cliMessage sanitizes the rendered message because the server's error detail reaches every Cli*
// helper through %s; the --output json envelope escapes it instead (escapeJSONControls). A \n
// from the server survives, so the detail can add a line that looks like
// our prefix; \r is dropped, so it cannot overwrite what is on screen. Arguments are stripped too,
// so color a value after sanitizing it, as cmd/server does a registration key, never before
// handing it to a Cli* helper.
func cliMessage(msg string, args ...any) string {
	clean, _ := SanitizeTerminalBlock(fmt.Sprintf(msg, args...))
	return clean
}

// WarnTerminalTextAltered takes a writer and an indent because its callers
// print outside the Cli* helpers and line the warning up with the value it is
// about.
func WarnTerminalTextAltered(w io.Writer, indent string) {
	_, _ = fmt.Fprintf(w, "%s%s: control characters from the server were removed from the text below.\n",
		indent, Yellow("Warning"))
}

// CliError handles all error messages in the CLI.
func CliError(msg string, args ...any) {
	printCliError(refusalErrorIn(args), msg, args...)
	reportCLIError()
}

// CliErrorWithExit handles all error messages in the CLI and exits 1, or 2 when an argument is a usage
// error. Under --output json it prints one envelope instead, classified from the first error in args, and no footer.
func CliErrorWithExit(msg string, args ...any) {
	ExitIfUsageError(ExitCodeGeneralError, "", nil, msg, args...)
	if OutputFormat == OutputFormatJSON {
		exitWithEnvelope(1, "", msg, args)
	}
	printCliError(refusalErrorIn(args), msg, args...)
	reportCLIError()
	os.Exit(1)
}

// CliErrorWithExitCode prints an error message to stderr and exits with the given code.
// Unlike CliErrorWithExit it omits the "report an issue" footer—use it for expected,
// machine-distinguishable refusals (e.g. a busy server) that scripts branch on by exit code.
func CliErrorWithExitCode(code int, msg string, args ...any) {
	ExitIfUsageError(code, "", nil, msg, args...)
	if OutputFormat == OutputFormatJSON {
		exitWithEnvelope(code, "", msg, args)
	}
	printCliError(refusalErrorIn(args), msg, args...)
	os.Exit(code)
}

// CliErrorWithCode names the json error_code (an ErrorCode* constant); table mode follows the
// exit code, so only exit 1 keeps the report footer.
func CliErrorWithCode(exitCode int, errorCode string, msg string, args ...any) {
	ExitIfUsageError(exitCode, "", nil, msg, args...)
	if OutputFormat == OutputFormatJSON {
		exitWithEnvelope(exitCode, errorCode, msg, args)
	}
	if exitCode == ExitCodeGeneralError {
		CliErrorWithExit(msg, args...)
	}
	CliErrorWithExitCode(exitCode, msg, args...)
}

// CliUsageErrorWithExit reports a flag or argument the command rejected and exits 2 with error_code usage_error.
func CliUsageErrorWithExit(msg string, args ...any) {
	CliUsageErrorEnvelopeWithExit("", msg, args...)
}

// CliParseErrorWithExit reports a command line cmd could not parse and exits 2 in format, the --output
// value its caller scanned from the arguments; table mode points to cmd's help.
func CliParseErrorWithExit(cmd *cobra.Command, format, msg string) {
	if format != "" {
		_ = SetOutputFormat(format) // an invalid value keeps the current format
	}
	if OutputFormat == OutputFormatJSON {
		CliUsageErrorWithExit("%s", msg)
	}
	CliUsageErrorWithExit("%s\nRun '%s --help' for usage.", msg, cmd.CommandPath())
}

func exitWithEnvelope(exitCode int, errorCode, msg string, args []any) {
	var cause error
	for _, arg := range args {
		if e, ok := arg.(error); ok && e != nil {
			cause = e
			break
		}
	}
	ExitWithFailureJSON(Failure{ExitCode: exitCode, ErrorCode: errorCode, Err: cause, Message: fmt.Sprintf(msg, args...)})
}

// printCliError writes the red Error line and, under it, the server's guidance
// for the refusal err carries (RefusalNextActions). The error helpers find err
// among their own arguments, so every call site that formats a server error
// into its message prints the server's hint without naming it.
func printCliError(err error, msg string, args ...any) {
	_, _ = fmt.Fprintf(TrackedStderr(), "%s: %s\n", Red("Error"), cliMessage(msg, args...))
	printRefusalNextActions(TrackedStderr(), err)
}

// DebugEnabled reports whether diagnostic output is turned on.
func DebugEnabled() bool {
	value, ok := os.LookupEnv(DebugEnvVar)
	if !ok {
		return false
	}
	value = strings.TrimSpace(value)
	return value != "" && !strings.EqualFold(value, "0") && !strings.EqualFold(value, "false")
}

// CliDebug prints a diagnostic message to stderr when DebugEnvVar is set. Use it for a rare branch
// with no visible symptom, such as a degrade that succeeds and so looks exactly like the path it
// replaced. It takes the same break off a spinner line as CliWarning, since the refresh-token
// degrade logs inside the "Refreshing access token..." spinner.
func CliDebug(msg string, args ...any) {
	if !DebugEnabled() {
		return
	}
	_, _ = fmt.Fprintf(TrackedStderr(), "%s%s: %s\n", spinnerLineBreak(), Yellow("Debug"), cliMessage(msg, args...))
}

// CliInfo handles all informational messages in the CLI.
func CliInfo(msg string, args ...any) {
	_, _ = fmt.Fprintf(TrackedStderr(), "%s: %s\n", Blue("Info"), cliMessage(msg, args...))
}

// CliWarning handles all warning messages in the CLI.
//
// A warning can land while a Spinner is animating stderr (SaveStreamAtomic
// warns from inside a download), and the spinner leaves the cursor mid-line.
// The break and the text go out as one write so a spinner frame cannot wedge
// itself between them.
func CliWarning(msg string, args ...any) {
	_, _ = fmt.Fprintf(TrackedStderr(), "%s%s: %s\n", spinnerLineBreak(), Yellow("Warning"), cliMessage(msg, args...))
	printRefusalNextActions(TrackedStderr(), refusalErrorIn(args))
}

// spinnerLineBreak returns the newline that moves a message off a line a spinner is animating, or
// "" when none is. A newline, not an erase escape: stdout and stderr share a cursor, so erasing the
// line would take unfinished streamed output with it; the last frame stays and the spinner redraws
// below. Spinner.StopWriter is not enough: it covers only call sites that opt in, while this backs
// every CliWarning and CliDebug site. Gated on a running spinner, not on a terminal: off a TTY the
// static progress line already ends in a newline.
//
// Only CliWarning and CliDebug take it, as the helpers that print inside a call a spinner wraps
// (SaveStreamAtomic's warning, the refresh degrade). CliInfo, CliSuccess and CliError print from
// the cmd layer or a prompt, after the api call owning the spinner returned. If one needs it, look
// at its new call site under a spinner first.
func spinnerLineBreak() string {
	if activeSpinners.Load() == 0 {
		return ""
	}
	return "\n"
}

// CliSuccess handles all success messages in the CLI.
func CliSuccess(msg string, args ...any) {
	_, _ = fmt.Fprintf(TrackedStderr(), "%s: %s\n", Green("Success"), cliMessage(msg, args...))
}

// CliInfoWithExit prints an informational message to stderr and exits the program with a status
// code of 0.
func CliInfoWithExit(msg string, args ...any) {
	_, _ = fmt.Fprintf(TrackedStderr(), "%s: %s\n", Blue("Info"), cliMessage(msg, args...))
	os.Exit(0)
}
