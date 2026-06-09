package utils

import (
	"fmt"
	"os"
)

// UsageErrorCode is the synthetic error_code for local validation failures that never reached the server.
const UsageErrorCode = "usage_error"

// cliErrorCtx mirrors the "operation" key of the mutation success outputs.
type cliErrorCtx struct {
	Operation string `json:"operation"`
}

// buildCliErrorEnvelope assembles the error envelope; an empty errorCode is omitted from the output.
func buildCliErrorEnvelope(operation, errorCode, message string) JSONErrorEnvelope[cliErrorCtx] {
	return JSONErrorEnvelope[cliErrorCtx]{
		OK:        false,
		ExitCode:  1,
		ErrorCode: errorCode,
		Message:   message,
		Context:   cliErrorCtx{Operation: operation},
	}
}

// buildCliErrorEnvelopeFromErr builds the envelope with the server error code extracted from err, if any.
func buildCliErrorEnvelopeFromErr(operation string, err error, message string) JSONErrorEnvelope[cliErrorCtx] {
	code := ""
	if err != nil {
		code, _ = ParseErrorResponse(err)
	}
	return buildCliErrorEnvelope(operation, code, message)
}

// CliErrorEnvelopeWithExit reports a command failure and exits(1): json mode prints the
// envelope to stderr with the server code from err; table mode matches CliErrorWithExit.
func CliErrorEnvelopeWithExit(operation string, err error, format string, args ...any) {
	if OutputFormat == OutputFormatJSON {
		PrintJSONError(os.Stderr, buildCliErrorEnvelopeFromErr(operation, err, fmt.Sprintf(format, args...)))
		os.Exit(1)
	}
	CliErrorWithExit(format, args...)
}

// CliUsageErrorEnvelopeWithExit is the local-validation variant; error_code is fixed to UsageErrorCode. Exits(1).
func CliUsageErrorEnvelopeWithExit(operation string, format string, args ...any) {
	if OutputFormat == OutputFormatJSON {
		PrintJSONError(os.Stderr, buildCliErrorEnvelope(operation, UsageErrorCode, fmt.Sprintf(format, args...)))
		os.Exit(1)
	}
	CliErrorWithExit(format, args...)
}
