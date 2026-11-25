package utils

import (
	"fmt"
	"time"
)

const mfaWaitMessage = "Waiting for MFA authentication..."

var maxRetryDuration = 3 * time.Minute

var retryInterval = 1 * time.Second

// ErrorHandlerCallbacks defines callback functions for handling different error types.
type ErrorHandlerCallbacks struct {
	OnMFARequired func(serverName string) error

	OnUsernameRequired func() error

	// OnWorkSessionRefused settles a WorkSession gate refusal, at most once. resend runs RetryOperation again;
	// failure replaces the refusal, and neither leaves the refusal as the result.
	OnWorkSessionRefused func(serverName string, err error) (resend bool, failure error)

	// RetryOperation re-runs the failed operation. After an MFA prompt it runs until it stops
	// failing with auth_mfa_required, so its refusal is what tells the wait MFA is not done yet.
	RetryOperation func() error
}

// HandleCommonErrors settles a tagged submission refusal (MarkSubmission) through callbacks and retries, returning nil on success.
// Any other error came after the operation took effect and is returned as is; the returned error carries no tag.
func HandleCommonErrors(err error, serverName string, callbacks ErrorHandlerCallbacks) error {
	if !IsSubmissionError(err) {
		return err
	}
	return endSubmission(handleSubmissionError(err, serverName, callbacks, nil))
}

func handleSubmissionError(err error, serverName string, callbacks ErrorHandlerCallbacks, restoreMFA func(string) error) error {
	if !IsSubmissionError(err) {
		return err
	}
	if isWorkSessionCode(StructuredErrorCode(err)) {
		if callbacks.OnWorkSessionRefused == nil || callbacks.RetryOperation == nil {
			return err
		}
		resend, failure := callbacks.OnWorkSessionRefused(serverName, err)
		if failure != nil {
			return failure
		}
		if !resend {
			return err
		}
		callbacks.OnWorkSessionRefused = nil
		if callbacks.OnMFARequired == nil {
			callbacks.OnMFARequired = restoreMFA
		}
		return handleSubmissionError(callbacks.RetryOperation(), serverName, callbacks, nil)
	}
	code, _ := ParseErrorResponse(err)

	switch code {
	case AuthMFARequired:
		if callbacks.OnMFARequired == nil {
			return err
		}

		if err := callbacks.OnMFARequired(serverName); err != nil {
			return NewExitError("MFA authentication failed: %s", err)
		}

		if callbacks.RetryOperation == nil {
			return err
		}
		next := retryUntilMFAAccepted(callbacks.RetryOperation)
		mfa := callbacks.OnMFARequired
		callbacks.OnMFARequired = nil
		return handleSubmissionError(next, serverName, callbacks, mfa)

	case UsernameRequired:
		if callbacks.OnUsernameRequired == nil {
			return err
		}

		if err := callbacks.OnUsernameRequired(); err != nil {
			return err
		}

		if callbacks.RetryOperation != nil {
			callbacks.OnUsernameRequired = nil
			return handleSubmissionError(callbacks.RetryOperation(), serverName, callbacks, restoreMFA)
		}
		return nil

	default:
		return err
	}
}

// retryUntilMFAAccepted re-runs retry once per retryInterval until the server stops refusing it for
// MFA. The step-up link names this client, so the next attempt passes with the same access token
// once MFA completes; no completion probe or token refresh is needed.
//
// Only the retried request's own tagged error (MarkSubmission) can keep the wait going: a typed
// auth_mfa_required, or an unprocessed attempt (IsUnprocessedRequestError) up to
// MaxConsecutivePollFailures in a row, its Retry-After setting the next gap within the poll backoff
// cap; unprocessed attempts are ridden through so a brief outage while the user is in the browser
// does not end the wait. Any other answer ends the wait, as does a gap that would pass the
// deadline.
func retryUntilMFAAccepted(retry func() error) error {
	spinner := NewSpinner(mfaWaitMessage)
	spinner.Start()
	defer spinner.Stop()

	startTime := time.Now()
	failures := 0
	delay := retryInterval
	for {
		// An attempt that would start past the deadline is never sent: the wait
		// ends instead, without sitting out a gap nothing follows.
		if remaining := maxRetryDuration - time.Since(startTime); delay > remaining {
			return fmt.Errorf("MFA authentication timed out after %v", maxRetryDuration)
		}

		time.Sleep(delay)

		// Any attempt may stream the command's output to stdout, which clearing the spinner's line
		// would erase, so the spinner is off during attempts and returns only after a refusal.
		spinner.Stop()
		err := retry()
		if err == nil {
			CliSuccess("MFA authentication completed")
			return nil
		}
		switch {
		case !IsSubmissionError(err):
			return err
		case StructuredErrorCode(err) == AuthMFARequired:
			failures = 0
			delay = retryInterval
		case IsUnprocessedRequestError(err):
			failures++
			if failures >= MaxConsecutivePollFailures {
				return err
			}
			delay = max(retryInterval, NextPollBackoff(retryInterval, 0, RetryAfter(err)))
		default:
			return err
		}
		// Off a TTY Start prints a static line rather than animating, and once
		// is enough for a log.
		if spinner.enabled {
			spinner.Start()
		}
	}
}
