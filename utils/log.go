package utils

import (
	"fmt"
	"os"
	"strings"
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

// CliError handles all error messages in the CLI.
func CliError(msg string, args ...any) {
	errorMessage := fmt.Sprintf(msg, args...)
	fmt.Fprintf(os.Stderr, "%s: %s\n", Red("Error"), errorMessage)
	reportCLIError()
}

// CliErrorWithExit handles all error messages in the CLI.
func CliErrorWithExit(msg string, args ...any) {
	errorMessage := fmt.Sprintf(msg, args...)
	fmt.Fprintf(os.Stderr, "%s: %s\n", Red("Error"), errorMessage)
	reportCLIError()
	os.Exit(1)
}

// CliErrorWithExitCode prints an error message to stderr and exits with the given code.
// Unlike CliErrorWithExit it omits the "report an issue" footer—use it for expected,
// machine-distinguishable refusals (e.g. a busy server) that scripts branch on by exit code.
func CliErrorWithExitCode(code int, msg string, args ...any) {
	errorMessage := fmt.Sprintf(msg, args...)
	fmt.Fprintf(os.Stderr, "%s: %s\n", Red("Error"), errorMessage)
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
	debugMessage := fmt.Sprintf(msg, args...)
	fmt.Fprintf(os.Stderr, "%s: %s\n", Yellow("Debug"), debugMessage)
}

// CliInfo handles all informational messages in the CLI.
func CliInfo(msg string, args ...any) {
	infoMessage := fmt.Sprintf(msg, args...)
	fmt.Fprintf(os.Stderr, "%s: %s\n", Blue("Info"), infoMessage)
}

// CliWarning handles all warning messages in the CLI.
func CliWarning(msg string, args ...any) {
	warningMessage := fmt.Sprintf(msg, args...)
	fmt.Fprintf(os.Stderr, "%s: %s\n", Yellow("Warning"), warningMessage)
}

// CliSuccess handles all success messages in the CLI.
func CliSuccess(msg string, args ...any) {
	successMessage := fmt.Sprintf(msg, args...)
	fmt.Fprintf(os.Stderr, "%s: %s\n", Green("Success"), successMessage)
}

// CliInfoWithExit prints an informational message to stderr and exits the program with a status code of 0
func CliInfoWithExit(msg string, args ...any) {
	infoMessage := fmt.Sprintf(msg, args...)
	fmt.Fprintf(os.Stderr, "%s: %s\n", Blue("Info"), infoMessage)
	os.Exit(0) // Use exit code 0 to indicate successful completion.
}
