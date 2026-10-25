package utils

import (
	"errors"
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

// buildCliUsageErrorEnvelope overrides the exit code buildCliErrorEnvelope
// hardcodes (1), so the envelope agrees with the process.
func buildCliUsageErrorEnvelope(operation, message string) JSONErrorEnvelope[cliErrorCtx] {
	envelope := buildCliErrorEnvelope(operation, UsageErrorCode, message)
	envelope.ExitCode = ExitCodeUsageError
	return envelope
}

// buildCliErrorEnvelopeFromErr builds the envelope with the server error code extracted from err, if any.
func buildCliErrorEnvelopeFromErr(operation string, err error, message string) JSONErrorEnvelope[cliErrorCtx] {
	code := ""
	if err != nil {
		code, _ = ParseErrorResponse(err)
	}
	envelope := buildCliErrorEnvelope(operation, code, message)
	envelope.NextActions = RefusalNextActions(err)
	return envelope
}

// CliErrorEnvelopeWithExit reports a failure and exits 1; json mode carries err's code and
// the server's hint as next_actions, table mode prints the hint under the error line. An
// ErrInvalidID refusal exits 2 through CliUsageErrorEnvelopeWithExit.
func CliErrorEnvelopeWithExit(operation string, err error, format string, args ...any) {
	if errors.Is(err, ErrInvalidID) {
		CliUsageErrorEnvelopeWithExit(operation, format, args...)
		return
	}
	guidance := refusalGuidance(err, args)
	if OutputFormat == OutputFormatJSON {
		envelope := buildCliErrorEnvelopeFromErr(operation, err, fmt.Sprintf(format, args...))
		envelope.NextActions = RefusalNextActions(guidance)
		PrintJSONError(os.Stderr, envelope)
		os.Exit(1)
	}
	printCliError(guidance, format, args...)
	reportCLIError()
	os.Exit(1)
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
	guidance := refusalGuidance(err, args)
	if OutputFormat == OutputFormatJSON {
		envelope := buildCliErrorEnvelopeFromErr(operation, err, fmt.Sprintf(format, args...))
		// buildCliErrorEnvelope hardcodes 1; leaving it would make the envelope
		// disagree with the process exit code.
		envelope.ExitCode = code
		envelope.NextActions = RefusalNextActions(guidance)
		PrintJSONError(os.Stderr, envelope)
		os.Exit(code)
	}
	printCliError(guidance, format, args...)
	os.Exit(code)
}

// CliUsageErrorEnvelopeWithExit reports a bad flag or argument and exits(2); error_code is fixed to UsageErrorCode.
func CliUsageErrorEnvelopeWithExit(operation string, format string, args ...any) {
	if OutputFormat == OutputFormatJSON {
		PrintJSONError(os.Stderr, buildCliUsageErrorEnvelope(operation, fmt.Sprintf(format, args...)))
		os.Exit(ExitCodeUsageError)
	}
	CliErrorWithExitCode(ExitCodeUsageError, format, args...)
}
