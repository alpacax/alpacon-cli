package utils

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// UsageErrorCode is the synthetic error_code for local validation failures that never reached the server.
const UsageErrorCode = "usage_error"

// CommandName is the full command path without the root name ("workspace ls"), set by the entry point.
var CommandName string

// cliErrorCtx mirrors the "operation" key of the mutation success outputs.
type cliErrorCtx struct {
	Operation string `json:"operation"`
	CommandID string `json:"command_id,omitempty"`
}

// newFailureEnvelope is the one builder behind every failure envelope. An empty errorCode
// is classified from err; an empty operation falls back to CommandName.
func newFailureEnvelope(exitCode int, errorCode, operation string, err error, message string) JSONErrorEnvelope[cliErrorCtx] {
	if errorCode == "" {
		errorCode = ClassifyError(err)
	}
	if operation == "" {
		operation = CommandName
	}
	return JSONErrorEnvelope[cliErrorCtx]{
		OK:         false,
		ExitCode:   exitCode,
		ErrorCode:  errorCode,
		Message:    message,
		Command:    CommandName,
		Context:    cliErrorCtx{Operation: operation},
		HTTPStatus: HTTPStatusCode(err),
		RetryAfter: retryAfterSeconds(RetryAfter(err)),
		Server:     ServerErrorFields(err),
	}
}

func retryAfterSeconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int((d + time.Second - 1) / time.Second)
}

func buildCliErrorEnvelope(operation, errorCode, message string) JSONErrorEnvelope[cliErrorCtx] {
	return newFailureEnvelope(1, errorCode, operation, nil, message)
}

func buildCliUsageErrorEnvelope(operation, message string) JSONErrorEnvelope[cliErrorCtx] {
	return newFailureEnvelope(ExitCodeUsageError, UsageErrorCode, operation, nil, message)
}

func buildCliErrorEnvelopeFromErr(operation string, err error, message string) JSONErrorEnvelope[cliErrorCtx] {
	return newFailureEnvelope(1, "", operation, err, message)
}

// CliErrorEnvelopeWithExit reports a failure and exits 1; json mode carries err's code. An
// ErrInvalidID refusal exits 2 through CliUsageErrorEnvelopeWithExit.
func CliErrorEnvelopeWithExit(operation string, err error, format string, args ...any) {
	if errors.Is(err, ErrInvalidID) {
		CliUsageErrorEnvelopeWithExit(operation, format, args...)
		return
	}
	if OutputFormat == OutputFormatJSON {
		PrintJSONError(os.Stderr, buildCliErrorEnvelopeFromErr(operation, err, fmt.Sprintf(format, args...)))
		os.Exit(1)
	}
	CliErrorWithExit(format, args...)
}

// CliErrorEnvelopeWithExitCode is CliErrorEnvelopeWithExit with a caller-chosen exit
// code, for refusals a script branches on (e.g. ExitCodeNotApproved).
func CliErrorEnvelopeWithExitCode(code int, operation string, err error, format string, args ...any) {
	if OutputFormat == OutputFormatJSON {
		errorCode := ""
		if code == ExitCodeNotApproved && ClassifyError(err) == ErrorCodeCLI {
			errorCode = ErrorCodeNotApproved
		}
		PrintJSONError(os.Stderr, newFailureEnvelope(code, errorCode, operation, err, fmt.Sprintf(format, args...)))
		os.Exit(code)
	}
	CliErrorWithExitCode(code, format, args...)
}

// CliUsageErrorEnvelopeWithExit reports a bad flag or argument and exits(2); error_code is fixed to UsageErrorCode.
func CliUsageErrorEnvelopeWithExit(operation string, format string, args ...any) {
	if OutputFormat == OutputFormatJSON {
		PrintJSONError(os.Stderr, buildCliUsageErrorEnvelope(operation, fmt.Sprintf(format, args...)))
		os.Exit(ExitCodeUsageError)
	}
	CliErrorWithExitCode(ExitCodeUsageError, format, args...)
}
