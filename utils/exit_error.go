package utils

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// ExitError is a failure a RunE command returns: cmd.Execute prints it with PrintExitError and exits
// with Code. Built from Code alone it prints nothing, for a command that wrote its own failure line.
type ExitError struct {
	Code      int
	usage     bool
	footer    bool
	operation string
	msg       string
	args      []any
}

func (e *ExitError) Error() string {
	if e.msg == "" {
		return fmt.Sprintf("exit status %d", e.Code)
	}
	return fmt.Sprintf(e.msg, e.args...)
}

// Unwrap returns the errors among the message's arguments, so a caller can still inspect the cause.
func (e *ExitError) Unwrap() []error {
	var causes []error
	for _, arg := range e.args {
		if err, ok := arg.(error); ok && err != nil {
			causes = append(causes, err)
		}
	}
	return causes
}

// NewExitError is the failure CliErrorWithExit reports: exit 1 with the issue footer, or 2 when an
// argument is a usage error.
func NewExitError(msg string, args ...any) error {
	return newExitError(ExitCodeGeneralError, true, msg, args)
}

// NewExitErrorWithCode is the failure CliErrorWithExitCode reports: the given exit and no footer.
func NewExitErrorWithCode(code int, msg string, args ...any) error {
	return newExitError(code, false, msg, args)
}

// NewUsageExitError is the failure CliUsageErrorWithExit reports: exit 2 with error_code usage_error.
func NewUsageExitError(msg string, args ...any) error {
	return newUsageExitError("", msg, args)
}

func newExitError(code int, footer bool, msg string, args []any) *ExitError {
	if code == ExitCodeGeneralError && usageErrorIn(args) {
		return newUsageExitError("", msg, args)
	}
	return &ExitError{Code: code, footer: footer, msg: msg, args: args}
}

func newUsageExitError(operation, msg string, args []any) *ExitError {
	return &ExitError{Code: ExitCodeUsageError, usage: true, operation: operation, msg: msg, args: args}
}

// PrintExitError writes e to stderr: the error line, the server's hint and the footer, or one
// envelope under --output json.
func PrintExitError(e *ExitError) {
	printExitError(TrackedStderr(), os.Stderr, e)
}

// FprintExitError writes what PrintExitError writes to w.
func FprintExitError(w io.Writer, e *ExitError) {
	printExitError(w, w, e)
}

// printExitError takes two writers because the envelope starts on a fresh line only when it is
// handed os.Stderr itself.
func printExitError(text, envelope io.Writer, e *ExitError) {
	if e.msg == "" {
		return
	}
	if OutputFormat == OutputFormatJSON {
		failure := envelopeFailure(e.Code, "", e.msg, e.args)
		if e.usage {
			failure = Failure{ExitCode: e.Code, ErrorCode: ErrorCodeUsage, Operation: e.operation, Message: failure.Message}
		}
		PrintJSONError(envelope, failure.envelope())
		return
	}
	printCliErrorTo(text, refusalErrorIn(e.args), e.msg, e.args...)
	if e.footer {
		printIssueFooter(text)
	}
}

func exitWithError(e *ExitError) {
	PrintExitError(e)
	os.Exit(e.Code)
}

// WithExitError wraps a RunE that returns ExitError. Cobra prints nothing for an ExitError, which
// cmd.Execute prints, but keeps its error line and usage for a flag or pre-run error.
func WithExitError(run func(*cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		err := run(cmd, args)
		if _, ok := errors.AsType[*ExitError](err); ok {
			cmd.SilenceErrors, cmd.SilenceUsage = true, true
		}
		return err
	}
}
