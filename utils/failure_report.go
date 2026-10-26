package utils

import (
	"os"
	"time"
)

// Failure describes a non-zero exit whose table-mode output the caller keeps; only the
// --output json envelope is built here. An empty ErrorCode is classified from Err.
type Failure struct {
	ExitCode       int
	ErrorCode      string
	Err            error
	Message        string
	Operation      string
	RemoteExitCode *int
	Partial        *PartialInfo
	NextActions    []NextAction
	HTTPStatus     int
	RetryAfter     time.Duration
	Server         map[string]any
}

func (f Failure) envelope() JSONErrorEnvelope[cliErrorCtx] {
	env := newFailureEnvelope(f.ExitCode, f.ErrorCode, f.Operation, f.Err, f.Message)
	env.RemoteExitCode = f.RemoteExitCode
	env.Partial = f.Partial
	env.NextActions = f.NextActions
	if f.HTTPStatus != 0 {
		env.HTTPStatus = f.HTTPStatus
	}
	if f.RetryAfter > 0 {
		env.RetryAfter = retryAfterSeconds(f.RetryAfter)
	}
	if f.Server != nil {
		env.Server = f.Server
	}
	return env
}

// ExitWithFailureJSON writes the envelope to stderr and exits with f.ExitCode under --output
// json; in table mode it returns so the caller's own message and exit stay as they were.
func ExitWithFailureJSON(f Failure) {
	if OutputFormat != OutputFormatJSON {
		return
	}
	PrintJSONError(os.Stderr, f.envelope())
	os.Exit(f.ExitCode)
}
