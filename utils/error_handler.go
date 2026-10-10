package utils

import (
	"fmt"
	"time"
)

// mfaWaitMessage labels the wait between showing the MFA link and the retried
// operation going through.
const mfaWaitMessage = "Waiting for MFA authentication..."

var maxRetryDuration = 3 * time.Minute

var retryInterval = 1 * time.Second

// ErrorHandlerCallbacks defines callback functions for handling different error types
type ErrorHandlerCallbacks struct {
	// OnMFARequired is called when MFA authentication is required
	// serverName: the name of the server requiring MFA
	OnMFARequired func(serverName string) error

	// OnUsernameRequired is called when username is required
	OnUsernameRequired func() error

	// RetryOperation re-runs the operation that failed. After an MFA prompt it is
	// called repeatedly until it stops failing with auth_mfa_required, so the
	// server's refusal is what tells the wait that MFA has not completed yet.
	// Should return nil on success, error on failure.
	RetryOperation func() error
}

// HandleCommonErrors handles common errors (MFA, UsernameRequired) with retry logic
// Returns nil if error was handled successfully, otherwise returns the original or new error
//
// An error marked by MarkProcessed is returned as is: the operation already took
// effect on the server, so nothing here may run it again.
func HandleCommonErrors(err error, serverName string, callbacks ErrorHandlerCallbacks) error {
	if IsProcessedError(err) {
		return err
	}
	code, _ := ParseErrorResponse(err)

	switch code {
	case AuthMFARequired:
		if callbacks.OnMFARequired == nil {
			return err
		}

		// Handle MFA error
		if err := callbacks.OnMFARequired(serverName); err != nil {
			CliErrorWithExit("MFA authentication failed: %s", err)
		}

		if callbacks.RetryOperation == nil {
			return err
		}
		return retryUntilMFAAccepted(callbacks.RetryOperation)

	case UsernameRequired:
		if callbacks.OnUsernameRequired == nil {
			return err
		}

		// Handle username required error
		if err := callbacks.OnUsernameRequired(); err != nil {
			return err
		}

		// Retry the operation if callback is provided
		if callbacks.RetryOperation != nil {
			return callbacks.RetryOperation()
		}
		return nil

	default:
		// Unknown error code, return original error
		return err
	}
}

// retryUntilMFAAccepted re-runs retry once per retryInterval until the server
// stops refusing it for MFA. The step-up link the user opened names this
// client, so completing MFA in the browser credits the client the request
// already comes from: the next attempt passes with the same access token, and
// no separate completion probe or token refresh is needed to notice.
//
// Only a typed auth_mfa_required keeps the wait going, never one read out of
// message text, and so does an attempt the server never acted on
// (IsUnprocessedRequestError), up to MaxConsecutivePollFailures in a row, so a
// brief outage while the user is still in the browser does not end it. A
// Retry-After on that answer sets the next gap, capped like any poll backoff;
// a gap that would carry the next attempt past the deadline ends the wait. An error marked by MarkProcessed came after
// the operation took effect and ends the wait whatever it carries. Any other
// answer, success or failure, is the operation's own result and ends the wait.
// The deadline bounds the attempts to about maxRetryDuration / retryInterval.
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

		// Any attempt may be the one that goes through and streams the
		// command's output to stdout, and a frame drawn over that output is
		// erased with it when the spinner clears its line. So the spinner is
		// off for every attempt and comes back only after a refusal.
		spinner.Stop()
		err := retry()
		if err == nil {
			CliSuccess("MFA authentication completed")
			return nil
		}
		switch {
		case IsProcessedError(err):
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
