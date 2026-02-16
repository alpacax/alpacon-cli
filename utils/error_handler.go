package utils

import (
	"fmt"
	"time"
)

const (
	maxRetryDuration = 1 * time.Minute
	retryInterval    = 5 * time.Second
)

// ErrorHandlerCallbacks defines callback functions for handling different error types
type ErrorHandlerCallbacks struct {
	// OnMFARequired is called when MFA authentication is required
	// serverName: the name of the server requiring MFA
	OnMFARequired func(serverName string) error

	// OnUsernameRequired is called when username is required
	OnUsernameRequired func() error

	// RetryOperation is called to retry the original operation after error handling
	// Should return nil on success, error on failure
	RetryOperation func() error
}

// HandleCommonErrors handles common errors (MFA, UsernameRequired) with retry logic
// Returns nil if error was handled successfully, otherwise returns the original or new error
func HandleCommonErrors(err error, serverName string, callbacks ErrorHandlerCallbacks) error {
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

		spinner := NewSpinner("Waiting for MFA authentication...")
		spinner.Start()

		startTime := time.Now()
		// Retry loop
		for {
			if time.Since(startTime) > maxRetryDuration {
				spinner.Stop()
				return fmt.Errorf("MFA authentication timed out after %v", maxRetryDuration)
			}

			time.Sleep(retryInterval)

			if callbacks.RetryOperation != nil {
				if err := callbacks.RetryOperation(); err == nil {
					spinner.Stop()
					CliSuccess("MFA authentication completed")
					return nil
				}
			} else {
				spinner.Stop()
				break
			}
		}

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

	return err
}
