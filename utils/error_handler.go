package utils

import (
	"fmt"
	"time"
)

var maxRetryDuration = 3 * time.Minute

var retryInterval = 1 * time.Second

type ErrorHandlerCallbacks struct {
	OnMFARequired func(serverName string) error

	OnUsernameRequired func() error

	// CheckMFACompleted is called to poll for MFA completion via a lightweight endpoint.
	// If nil, falls back to the legacy RefreshToken+RetryOperation loop.
	CheckMFACompleted func() (bool, error)

	// RefreshToken is called before each MFA retry to refresh the access token
	// so the server can see the latest MFA completion state
	RefreshToken func() error

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

		if err := callbacks.OnMFARequired(serverName); err != nil {
			CliErrorWithExit("MFA authentication failed: %s", err)
		}

		spinner := NewSpinner("Waiting for MFA authentication...")
		spinner.Start()

		startTime := time.Now()

		if callbacks.CheckMFACompleted != nil {
			// New flow: poll lightweight completion endpoint, then retry once
			for {
				if time.Since(startTime) > maxRetryDuration {
					spinner.Stop()
					return fmt.Errorf("MFA authentication timed out after %v", maxRetryDuration)
				}

				time.Sleep(retryInterval)

				completed, err := callbacks.CheckMFACompleted()
				if err != nil {
					// Non-fatal: endpoint may not be deployed yet, keep polling
					continue
				}
				if !completed {
					continue
				}

				// MFA completed — refresh token and retry once
				if callbacks.RefreshToken != nil {
					if err := callbacks.RefreshToken(); err != nil {
						spinner.Stop()
						return fmt.Errorf("failed to refresh token; please run 'alpacon login' to re-authenticate: %w", err)
					}
				}

				if callbacks.RetryOperation != nil {
					if err := callbacks.RetryOperation(); err != nil {
						spinner.Stop()
						return err
					}
				}

				spinner.Stop()
				CliSuccess("MFA authentication completed")
				return nil
			}
		} else {
			// Legacy flow: RefreshToken + RetryOperation loop
			for {
				if time.Since(startTime) > maxRetryDuration {
					spinner.Stop()
					return fmt.Errorf("MFA authentication timed out after %v", maxRetryDuration)
				}

				time.Sleep(retryInterval)

				if callbacks.RefreshToken != nil {
					if err := callbacks.RefreshToken(); err != nil {
						spinner.Stop()
						return fmt.Errorf("failed to refresh token; please run 'alpacon login' to re-authenticate: %w", err)
					}
				}

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
		}

	case UsernameRequired:
		if callbacks.OnUsernameRequired == nil {
			return err
		}

		if err := callbacks.OnUsernameRequired(); err != nil {
			return err
		}

		if callbacks.RetryOperation != nil {
			return callbacks.RetryOperation()
		}
		return nil

	default:
		return err
	}

	return err
}
