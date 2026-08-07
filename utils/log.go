package utils

import (
	"fmt"
	"os"
)

const (
	gitIssueURL = "https://github.com/alpacax/alpacon-cli/issues"
)

func reportCLIError() {
	fmt.Fprintln(os.Stderr, "For issues, check the latest version or report on", gitIssueURL)
}

func CliError(msg string, args ...any) {
	errorMessage := fmt.Sprintf(msg, args...)
	fmt.Fprintf(os.Stderr, "%s: %s\n", Red("Error"), errorMessage)
	reportCLIError()
}

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

func CliInfo(msg string, args ...any) {
	infoMessage := fmt.Sprintf(msg, args...)
	fmt.Fprintf(os.Stderr, "%s: %s\n", Blue("Info"), infoMessage)
}

func CliWarning(msg string, args ...any) {
	warningMessage := fmt.Sprintf(msg, args...)
	fmt.Fprintf(os.Stderr, "%s: %s\n", Yellow("Warning"), warningMessage)
}

func CliSuccess(msg string, args ...any) {
	successMessage := fmt.Sprintf(msg, args...)
	fmt.Fprintf(os.Stderr, "%s: %s\n", Green("Success"), successMessage)
}

func CliInfoWithExit(msg string, args ...any) {
	infoMessage := fmt.Sprintf(msg, args...)
	fmt.Fprintf(os.Stderr, "%s: %s\n", Blue("Info"), infoMessage)
	os.Exit(0)
}
