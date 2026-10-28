package utils

import (
	"fmt"
	"os"
	"time"
)

// CommandName is the full command path without the root name ("workspace ls"), set by the entry point.
var CommandName string

// cliErrorCtx mirrors the "operation" key of the mutation success outputs.
type cliErrorCtx struct {
	Operation string `json:"operation"`
}

// newFailureEnvelope classifies an empty errorCode from err, then from exitCode; an empty
// operation falls back to CommandName.
func newFailureEnvelope(exitCode int, errorCode, operation string, err error, message string) JSONErrorEnvelope[cliErrorCtx] {
	if errorCode == "" {
		if errorCode = ClassifyError(err); errorCode == ErrorCodeCLI {
			errorCode = exitCodeFallback(exitCode)
		}
	}
	if operation == "" {
		operation = CommandName
	}
	return JSONErrorEnvelope[cliErrorCtx]{
		OK:          false,
		ExitCode:    exitCode,
		ErrorCode:   errorCode,
		Message:     message,
		Context:     cliErrorCtx{Operation: operation},
		HTTPStatus:  HTTPStatusCode(err),
		RetryAfter:  retryAfterSeconds(RetryAfter(err)),
		Server:      serverErrorFields(err),
		NextActions: RefusalNextActions(err),
	}
}

// exitCodeFallback names the code an exit code implies when err says nothing more.
func exitCodeFallback(exitCode int) string {
	switch exitCode {
	case ExitCodeUsageError:
		return ErrorCodeUsage
	case ExitCodeNotApproved:
		return ErrorCodeNotApproved
	}
	return ErrorCodeCLI
}

func retryAfterSeconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	seconds := d / time.Second
	if d%time.Second != 0 {
		seconds++
	}
	return int(seconds)
}

// CliErrorEnvelopeWithExit reports a failure and exits 1, or 2 when err or an argument is a usage error;
// json mode carries err's code.
func CliErrorEnvelopeWithExit(operation string, err error, format string, args ...any) {
	ExitIfUsageError(ExitCodeGeneralError, operation, err, format, args...)
	ExitWithFailureJSON(Failure{ExitCode: ExitCodeGeneralError, Operation: operation, Err: err, Message: fmt.Sprintf(format, args...), NextActions: RefusalNextActions(refusalGuidance(err, args))})
	printCliError(refusalGuidance(err, args), format, args...)
	reportCLIError()
	os.Exit(ExitCodeGeneralError)
}

// refusalGuidance is the error whose server hints an envelope helper shows:
// err, or, when a caller passes nil to keep a code out of the envelope, the
// refusal among the message's own arguments. The hint is guidance, not the
// code, so it shows either way.
func refusalGuidance(err error, args []any) error {
	if err != nil {
		return err
	}
	return refusalErrorIn(args)
}

// CliErrorEnvelopeWithExitCode is CliErrorEnvelopeWithExit with a caller-chosen exit
// code, for refusals a script branches on (e.g. ExitCodeNotApproved).
func CliErrorEnvelopeWithExitCode(code int, operation string, err error, format string, args ...any) {
	ExitIfUsageError(code, operation, err, format, args...)
	ExitWithFailureJSON(Failure{ExitCode: code, Operation: operation, Err: err, Message: fmt.Sprintf(format, args...), NextActions: RefusalNextActions(refusalGuidance(err, args))})
	printCliError(refusalGuidance(err, args), format, args...)
	os.Exit(code)
}

// ExitIfUsageError is the one place a report that would exit 1 exits 2 instead: when err, or an error
// among args, answers IsUsageError, it reports through CliUsageErrorEnvelopeWithExit. Otherwise it returns.
func ExitIfUsageError(code int, operation string, err error, format string, args ...any) {
	if code == ExitCodeGeneralError && (IsUsageError(err) || usageErrorIn(args)) {
		CliUsageErrorEnvelopeWithExit(operation, format, args...)
	}
}

// CliUsageErrorEnvelopeWithExit reports a bad flag or argument and exits 2 with error_code usage_error.
// The envelope keeps no error from args, so a server refusal must not go through it.
func CliUsageErrorEnvelopeWithExit(operation string, format string, args ...any) {
	ExitWithFailureJSON(Failure{ExitCode: ExitCodeUsageError, ErrorCode: ErrorCodeUsage, Operation: operation, Message: fmt.Sprintf(format, args...)})
	CliErrorWithExitCode(ExitCodeUsageError, format, args...)
}
