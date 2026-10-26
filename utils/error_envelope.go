package utils

import (
	"errors"
	"fmt"
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
		OK:         false,
		ExitCode:   exitCode,
		ErrorCode:  errorCode,
		Message:    message,
		Context:    cliErrorCtx{Operation: operation},
		HTTPStatus: HTTPStatusCode(err),
		RetryAfter: retryAfterSeconds(RetryAfter(err)),
		Server:     ServerErrorFields(err),
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

// CliErrorEnvelopeWithExit reports a failure and exits 1; json mode carries err's code. An
// ErrInvalidID refusal exits 2 through CliUsageErrorEnvelopeWithExit.
func CliErrorEnvelopeWithExit(operation string, err error, format string, args ...any) {
	if errors.Is(err, ErrInvalidID) {
		CliUsageErrorEnvelopeWithExit(operation, format, args...)
		return
	}
	ExitWithFailureJSON(Failure{ExitCode: ExitCodeGeneralError, Operation: operation, Err: err, Message: fmt.Sprintf(format, args...)})
	CliErrorWithExit(format, args...)
}

// CliErrorEnvelopeWithExitCode is CliErrorEnvelopeWithExit with a caller-chosen exit
// code, for refusals a script branches on (e.g. ExitCodeNotApproved).
func CliErrorEnvelopeWithExitCode(code int, operation string, err error, format string, args ...any) {
	ExitWithFailureJSON(Failure{ExitCode: code, Operation: operation, Err: err, Message: fmt.Sprintf(format, args...)})
	CliErrorWithExitCode(code, format, args...)
}

// CliUsageErrorEnvelopeWithExit reports a bad flag or argument and exits 2 with error_code usage_error.
func CliUsageErrorEnvelopeWithExit(operation string, format string, args ...any) {
	ExitWithFailureJSON(Failure{ExitCode: ExitCodeUsageError, ErrorCode: ErrorCodeUsage, Operation: operation, Message: fmt.Sprintf(format, args...)})
	CliErrorWithExitCode(ExitCodeUsageError, format, args...)
}
