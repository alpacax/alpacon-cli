package utils

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// ExitError is a failure a RunE command returns: cmd.Execute prints it with PrintExitError and exits
// with Code. Build it with a constructor; NewSilentExitError is the one that prints nothing.
type ExitError struct {
	Code      int
	silent    bool
	usage     bool
	footer    bool
	envelope  bool // built by NewEnvelopeExitError: json mode classifies cause under operation
	operation string
	errorCode string // the json error_code when the failure names one instead of classifying it
	cause     error
	msg       string
	args      []any
	print     func(text, envelope io.Writer) // replaces the error line for a failure with its own layout
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
	if e.cause != nil {
		causes = append(causes, e.cause)
	}
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

// NewExitErrorWithErrorCode names the json error_code (an ErrorCode* constant); table mode follows the
// exit code, so only exit 1 keeps the report footer.
func NewExitErrorWithErrorCode(exitCode int, errorCode, msg string, args ...any) error {
	e := newExitError(exitCode, exitCode == ExitCodeGeneralError, msg, args)
	e.errorCode = errorCode
	return e
}

// NewSilentExitError exits with code and prints nothing, for a command that wrote its own failure line.
func NewSilentExitError(code int) error {
	return &ExitError{Code: code, silent: true}
}

// NewUsageExitError is the failure CliUsageErrorWithExit reports: exit 2 with error_code usage_error.
func NewUsageExitError(msg string, args ...any) error {
	return newUsageExitError("", msg, args)
}

// NewEnvelopeExitError is the failure CliErrorEnvelopeWithExit reports: exit 1 with the footer, or 2
// when err or an argument is a usage error; json mode classifies err under operation.
func NewEnvelopeExitError(operation string, err error, msg string, args ...any) error {
	return newEnvelopeExitError(ExitCodeGeneralError, true, operation, err, msg, args)
}

// NewEnvelopeExitErrorWithCode is the failure CliErrorEnvelopeWithExitCode reports: the given exit and no
// footer, or exit 2 when code is 1 and err or an argument is a usage error.
func NewEnvelopeExitErrorWithCode(code int, operation string, err error, msg string, args ...any) error {
	return newEnvelopeExitError(code, false, operation, err, msg, args)
}

// NewUsageEnvelopeExitError is the failure CliUsageErrorEnvelopeWithExit reports.
func NewUsageEnvelopeExitError(operation, msg string, args ...any) error {
	return newUsageExitError(operation, msg, args)
}

func newEnvelopeExitError(code int, footer bool, operation string, err error, msg string, args []any) *ExitError {
	if code == ExitCodeGeneralError && (IsUsageError(err) || usageErrorIn(args)) {
		return newUsageExitError(operation, msg, args)
	}
	return &ExitError{Code: code, footer: footer, envelope: true, operation: operation, cause: err, msg: msg, args: args}
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
	signalExitError(e)
	printExitError(TrackedStderr(), os.Stderr, e)
}

// signalExitError signals what the legacy helpers signaled on the same failure: the first error under
// --output json, unless it is a usage error, and the refusal the error line reports otherwise.
func signalExitError(e *ExitError) {
	if e.silent || e.print != nil {
		return
	}
	if OutputFormat == OutputFormatJSON {
		if e.envelope && e.cause != nil {
			SignalPaused(e.cause)
		} else if !e.usage {
			SignalPaused(envelopeFailure(e.Code, "", e.msg, e.args).Err)
		}
		return
	}
	signalCliError(refusalGuidance(e.cause, e.args), e.args)
}

// FprintExitError writes what PrintExitError writes to w.
func FprintExitError(w io.Writer, e *ExitError) {
	printExitError(w, w, e)
}

// printExitError takes two writers because the envelope starts on a fresh line only when it is
// handed os.Stderr itself.
func printExitError(text, envelope io.Writer, e *ExitError) {
	if e.silent {
		return
	}
	if e.print != nil {
		e.print(text, envelope)
		return
	}
	if OutputFormat == OutputFormatJSON {
		failure := envelopeFailure(e.Code, e.errorCode, e.msg, e.args)
		switch {
		case e.usage:
			failure.ErrorCode, failure.Operation, failure.Err = ErrorCodeUsage, e.operation, nil
		case e.envelope:
			failure = Failure{ExitCode: e.Code, Operation: e.operation, Err: e.cause, Message: failure.Message, NextActions: RefusalNextActions(refusalGuidance(e.cause, e.args))}
		}
		PrintJSONError(envelope, failure.envelope())
		return
	}
	printCliErrorTo(text, refusalGuidance(e.cause, e.args), e.msg, e.args...)
	if e.footer {
		printIssueFooter(text, append([]any{e.cause}, e.args...)...)
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
