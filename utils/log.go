package utils

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

const (
	gitIssueURL = "https://github.com/alpacax/alpacon-cli/issues"

	// DebugEnvVar turns on diagnostic output that would be noise in normal use.
	// Any value other than "0" or "false" enables it, matching how the other
	// ALPACON_* switches are read.
	DebugEnvVar = "ALPACON_DEBUG"
)

func reportCLIError() {
	fmt.Fprintln(os.Stderr, "For issues, check the latest version or report on", gitIssueURL)
}

// cliMessage sanitizes a rendered message before it reaches the terminal:
// client.parseAPIError puts the server's error detail into every one of these
// helpers through %s, and the --output json envelope is the only path escaping
// it today (escapeJSONControls).
//
// Per line, because SanitizeTerminalText drops \n and callers pass multi-line
// guidance. Splitting after the format is rendered means a \n from the server
// survives too, so the detail can add a line that looks like our own prefix.
// Accepted: \r is still dropped, so it can only append below, never overwrite
// what is already on screen.
//
// The arguments go through the same strip, so never hand a Color()-wrapped
// value to a Cli* helper—it loses its highlight. Colorize after sanitizing,
// the way cmd/server prints a registration key.
func cliMessage(msg string, args ...any) string {
	lines := strings.Split(fmt.Sprintf(msg, args...), "\n")
	for i, line := range lines {
		lines[i] = SanitizeTerminalText(line)
	}
	return strings.Join(lines, "\n")
}

// CliError handles all error messages in the CLI.
func CliError(msg string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s: %s\n", Red("Error"), cliMessage(msg, args...))
	reportCLIError()
}

// CliErrorWithExit handles all error messages in the CLI.
func CliErrorWithExit(msg string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s: %s\n", Red("Error"), cliMessage(msg, args...))
	reportCLIError()
	os.Exit(1)
}

// CliErrorWithExitCode prints an error message to stderr and exits with the given code.
// Unlike CliErrorWithExit it omits the "report an issue" footer—use it for expected,
// machine-distinguishable refusals (e.g. a busy server) that scripts branch on by exit code.
func CliErrorWithExitCode(code int, msg string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s: %s\n", Red("Error"), cliMessage(msg, args...))
	os.Exit(code)
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

// CliDebug prints a diagnostic message to stderr when DebugEnvVar is set.
//
// Use it for a branch that is expected to be rare and that produces no visible
// symptom when it runs: a degrade that always succeeds looks exactly like the
// path it replaced, so without a line here there is nothing to tell the two
// apart after the fact.
func CliDebug(msg string, args ...any) {
	if !DebugEnabled() {
		return
	}
	fmt.Fprintf(os.Stderr, "%s: %s\n", Yellow("Debug"), cliMessage(msg, args...))
}

// CliInfo handles all informational messages in the CLI.
func CliInfo(msg string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s: %s\n", Blue("Info"), cliMessage(msg, args...))
}

// CliWarning handles all warning messages in the CLI.
//
// A warning can land while a Spinner is animating stderr (SaveStreamAtomic
// warns from inside a download), and the spinner leaves the cursor mid-line.
// The clear and the text go out as one write so a spinner frame cannot wedge
// itself between them.
func CliWarning(msg string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s%s: %s\n", clearStderrLine(), Yellow("Warning"), cliMessage(msg, args...))
}

// clearStderrLine returns the escape that wipes whatever is on the current
// line, or "" when stderr is not a terminal—there is no spinner animation to
// clear, and the escape would only be noise in a log.
func clearStderrLine() string {
	if !term.IsTerminal(int(os.Stderr.Fd())) {
		return ""
	}
	return "\r\033[K"
}

// CliSuccess handles all success messages in the CLI.
func CliSuccess(msg string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s: %s\n", Green("Success"), cliMessage(msg, args...))
}

// CliInfoWithExit prints an informational message to stderr and exits the program with a status code of 0
func CliInfoWithExit(msg string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s: %s\n", Blue("Info"), cliMessage(msg, args...))
	os.Exit(0) // Use exit code 0 to indicate successful completion.
}
