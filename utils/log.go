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
