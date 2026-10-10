package utils

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withFastRetry(t *testing.T) {
	t.Helper()
	orig := retryInterval
	retryInterval = 10 * time.Millisecond
	t.Cleanup(func() { retryInterval = orig })
}

func withFastTimeout(t *testing.T) {
	t.Helper()
	orig := maxRetryDuration
	maxRetryDuration = 200 * time.Millisecond
	t.Cleanup(func() { maxRetryDuration = orig })
}

func TestHandleCommonErrors_UnknownError(t *testing.T) {
	t.Parallel()
	err := errors.New("connection refused")
	result := HandleCommonErrors(err, "server1", ErrorHandlerCallbacks{})
	assert.Equal(t, err, result)
}

// A server that cannot do MFA sign-in has nothing to step up to, so the refusal
// must come back unchanged without running any callback.
func TestHandleCommonErrors_SudoWithMFAStepUpUnavailableStartsNoStepUp(t *testing.T) {
	t.Parallel()
	err := errors.New(`{"code": "workspace_sudo_with_mfa_step_up_unavailable", "gate": "presence"}`)
	var called atomic.Bool
	mark := func() { called.Store(true) }
	result := HandleCommonErrors(err, "server1", ErrorHandlerCallbacks{
		OnMFARequired:      func(string) error { mark(); return nil },
		OnUsernameRequired: func() error { mark(); return nil },
		RetryOperation:     func() error { mark(); return nil },
	})
	assert.Equal(t, err, result)
	assert.False(t, called.Load())
	assert.Equal(t, WorkspaceSudoWithMFAStepUpUnavailable, "workspace_sudo_with_mfa_step_up_unavailable")
}

// A credential that cannot prove MFA gains nothing from a browser step-up, so
// the refusal must come back unchanged without running any callback.
func TestHandleCommonErrors_CredentialCannotProveMFAStartsNoStepUp(t *testing.T) {
	t.Parallel()
	err := errors.New(`{"code": "sudo_verify_credential_cannot_prove_mfa", "gate": "presence"}`)
	var called atomic.Bool
	mark := func() { called.Store(true) }
	result := HandleCommonErrors(err, "server1", ErrorHandlerCallbacks{
		OnMFARequired:      func(string) error { mark(); return nil },
		OnUsernameRequired: func() error { mark(); return nil },
		RetryOperation:     func() error { mark(); return nil },
	})
	assert.Equal(t, err, result)
	assert.False(t, called.Load())
}

func TestHandleCommonErrors_UsernameRequired(t *testing.T) {
	t.Parallel()
	t.Run("no callback returns original error", func(t *testing.T) {
		err := errors.New(`{"code": "user_username_required", "source": ""}`)
		result := HandleCommonErrors(err, "server1", ErrorHandlerCallbacks{})
		assert.Equal(t, err, result)
	})

	t.Run("callback succeeds with retry", func(t *testing.T) {
		err := errors.New(`{"code": "user_username_required", "source": ""}`)
		result := HandleCommonErrors(err, "server1", ErrorHandlerCallbacks{
			OnUsernameRequired: func() error { return nil },
			RetryOperation:     func() error { return nil },
		})
		assert.NoError(t, result)
	})

	t.Run("callback fails", func(t *testing.T) {
		err := errors.New(`{"code": "user_username_required", "source": ""}`)
		cbErr := errors.New("username prompt failed")
		result := HandleCommonErrors(err, "server1", ErrorHandlerCallbacks{
			OnUsernameRequired: func() error { return cbErr },
		})
		assert.Equal(t, cbErr, result)
	})
}

func TestHandleCommonErrors_MFA_NoCallback(t *testing.T) {
	t.Parallel()
	err := errors.New(`{"code": "auth_mfa_required", "source": "command"}`)
	result := HandleCommonErrors(err, "server1", ErrorHandlerCallbacks{})
	assert.Equal(t, err, result)
}

// codedMFARefusal is the refusal as a structured server error carries it; the wait
// keeps going only on this form, never on text that merely quotes the code.
type codedMFARefusal struct{}

func (codedMFARefusal) Error() string       { return mfaRefusal }
func (codedMFARefusal) ErrorCode() string   { return AuthMFARequired }
func (codedMFARefusal) ErrorSource() string { return "command" }

const mfaRefusal = `{"code": "auth_mfa_required", "source": "command"}`

func TestHandleCommonErrors_MFA_RetriesUntilTheRequestPasses(t *testing.T) {
	withFastRetry(t)
	var retryCount atomic.Int32

	result := HandleCommonErrors(errors.New(mfaRefusal), "server1", ErrorHandlerCallbacks{
		OnMFARequired: func(string) error { return nil },
		RetryOperation: func() error {
			if retryCount.Add(1) < 3 {
				return codedMFARefusal{}
			}
			return nil
		},
	})

	require.NoError(t, result)
	assert.Equal(t, int32(3), retryCount.Load(), "retry through the refusals and stop on the first success")
}

// Only auth_mfa_required means MFA has not landed yet. Anything else is the
// operation's own answer and ends the wait at once, unretried.
func TestHandleCommonErrors_MFA_AnotherErrorEndsTheWait(t *testing.T) {
	withFastRetry(t)
	withFastTimeout(t)
	retryErr := errors.New("session creation failed")
	var retryCount atomic.Int32

	result := HandleCommonErrors(errors.New(mfaRefusal), "server1", ErrorHandlerCallbacks{
		OnMFARequired: func(string) error { return nil },
		RetryOperation: func() error {
			if retryCount.Add(1) == 1 {
				return codedMFARefusal{}
			}
			return retryErr
		},
	})

	assert.Equal(t, retryErr, result)
	assert.Equal(t, int32(2), retryCount.Load(), "the error must not be retried")
}

func TestHandleCommonErrors_MFA_TimesOutWhileStillRefused(t *testing.T) {
	withFastRetry(t)
	withFastTimeout(t)
	var retryCount atomic.Int32

	result := HandleCommonErrors(errors.New(mfaRefusal), "server1", ErrorHandlerCallbacks{
		OnMFARequired: func(string) error { return nil },
		RetryOperation: func() error {
			retryCount.Add(1)
			return codedMFARefusal{}
		},
	})

	require.ErrorContains(t, result, "MFA authentication timed out")
	assert.Positive(t, retryCount.Load())
	// 200ms at a 10ms interval, plus one attempt of slack for the deadline check.
	assert.LessOrEqual(t, retryCount.Load(), int32(21), "the timeout must bound the retries")
}

func TestHandleCommonErrors_MFA_NoRetryReturnsTheRefusal(t *testing.T) {
	t.Parallel()
	err := errors.New(mfaRefusal)
	var prompted atomic.Bool

	result := HandleCommonErrors(err, "server1", ErrorHandlerCallbacks{
		OnMFARequired: func(string) error { prompted.Store(true); return nil },
	})

	assert.Equal(t, err, result)
	assert.True(t, prompted.Load(), "the link is still worth showing")
}

type statusError int

func (e statusError) Error() string       { return fmt.Sprintf("HTTP %d", int(e)) }
func (e statusError) HTTPStatusCode() int { return int(e) }

// A brief outage while the user is still in the browser does not end the wait:
// attempts the server never acted on are ridden through.
func TestHandleCommonErrors_MFA_RidesThroughUnprocessedAttempts(t *testing.T) {
	withFastRetry(t)
	var retryCount atomic.Int32

	result := HandleCommonErrors(errors.New(mfaRefusal), "server1", ErrorHandlerCallbacks{
		OnMFARequired: func(string) error { return nil },
		RetryOperation: func() error {
			switch retryCount.Add(1) {
			case 1, 2:
				return statusError(http.StatusServiceUnavailable)
			case 3:
				return codedMFARefusal{}
			default:
				return nil
			}
		},
	})

	require.NoError(t, result)
	assert.Equal(t, int32(4), retryCount.Load())
}

// The ride-through is bounded: that many unprocessed attempts in a row end the
// wait with the last one's error.
func TestHandleCommonErrors_MFA_EndsAfterConsecutiveUnprocessedAttempts(t *testing.T) {
	withFastRetry(t)
	var retryCount atomic.Int32

	result := HandleCommonErrors(errors.New(mfaRefusal), "server1", ErrorHandlerCallbacks{
		OnMFARequired: func(string) error { return nil },
		RetryOperation: func() error {
			retryCount.Add(1)
			return statusError(http.StatusTooManyRequests)
		},
	})

	assert.Equal(t, http.StatusTooManyRequests, HTTPStatusCode(result))
	assert.Equal(t, int32(MaxConsecutivePollFailures), retryCount.Load())
}

// A 502 or 504 can follow a request that already ran, so it ends the wait
// rather than being sent again.
func TestHandleCommonErrors_MFA_GatewayErrorEndsTheWait(t *testing.T) {
	withFastRetry(t)
	var retryCount atomic.Int32

	result := HandleCommonErrors(errors.New(mfaRefusal), "server1", ErrorHandlerCallbacks{
		OnMFARequired: func(string) error { return nil },
		RetryOperation: func() error {
			retryCount.Add(1)
			return statusError(http.StatusBadGateway)
		},
	})

	assert.Equal(t, http.StatusBadGateway, HTTPStatusCode(result))
	assert.Equal(t, int32(1), retryCount.Load())
}

func TestIsUnprocessedRequestError(t *testing.T) {
	t.Parallel()
	dial := &url.Error{Op: "Post", URL: "https://x", Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}
	read := &url.Error{Op: "Post", URL: "https://x", Err: &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset")}}

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"dial failure", dial, true},
		{"wrapped dial failure", fmt.Errorf("submit: %w", dial), true},
		{"read failure after sending", read, false},
		{"429", statusError(http.StatusTooManyRequests), true},
		{"503", statusError(http.StatusServiceUnavailable), true},
		{"502", statusError(http.StatusBadGateway), false},
		{"504", statusError(http.StatusGatewayTimeout), false},
		{"403", statusError(http.StatusForbidden), false},
		{"plain error", errors.New("boom"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, IsUnprocessedRequestError(tc.err))
		})
	}
}

// A message that only quotes the code, such as a transfer failure an agent
// reported, is the operation's own answer and ends the wait.
func TestHandleCommonErrors_MFA_QuotedCodeInTextEndsTheWait(t *testing.T) {
	withFastRetry(t)
	var retryCount atomic.Int32
	quoted := fmt.Errorf("transfer failed: %s", mfaRefusal)

	result := HandleCommonErrors(errors.New(mfaRefusal), "server1", ErrorHandlerCallbacks{
		OnMFARequired: func(string) error { return nil },
		RetryOperation: func() error {
			retryCount.Add(1)
			return quoted
		},
	})

	assert.Equal(t, quoted, result)
	assert.Equal(t, int32(1), retryCount.Load())
}

// An error after the server accepted the submission is never read as a refused
// one, whatever status or dial failure it carries, so the operation is not sent again.
func TestHandleCommonErrors_MFA_ProcessedErrorEndsTheWait(t *testing.T) {
	withFastRetry(t)
	dial := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	for name, cause := range map[string]error{"503": statusError(http.StatusServiceUnavailable), "429": statusError(http.StatusTooManyRequests), "dial": dial} {
		t.Run(name, func(t *testing.T) {
			var retryCount atomic.Int32
			late := MarkProcessed(fmt.Errorf("failed to read command output: %w", cause))

			result := HandleCommonErrors(errors.New(mfaRefusal), "server1", ErrorHandlerCallbacks{
				OnMFARequired: func(string) error { return nil },
				RetryOperation: func() error {
					retryCount.Add(1)
					return late
				},
			})

			assert.Equal(t, late, result)
			assert.Equal(t, int32(1), retryCount.Load(), "the accepted submission must not be sent again")
			assert.EqualError(t, late, "failed to read command output: "+cause.Error(), "marking must not change the message")
		})
	}
}

type retryAfterStatusError struct {
	statusError
	after time.Duration
}

func (e retryAfterStatusError) RetryAfter() time.Duration { return e.after }

// A 429 waits the Retry-After the server sent, not the usual interval.
func TestHandleCommonErrors_MFA_HonorsRetryAfterOn429(t *testing.T) {
	withFastRetry(t)
	var retryCount atomic.Int32
	var stamps []time.Time

	result := HandleCommonErrors(errors.New(mfaRefusal), "server1", ErrorHandlerCallbacks{
		OnMFARequired: func(string) error { return nil },
		RetryOperation: func() error {
			stamps = append(stamps, time.Now())
			if retryCount.Add(1) == 1 {
				return retryAfterStatusError{statusError(http.StatusTooManyRequests), 150 * time.Millisecond}
			}
			return nil
		},
	})

	require.NoError(t, result)
	require.Len(t, stamps, 2)
	assert.GreaterOrEqual(t, stamps[1].Sub(stamps[0]), 150*time.Millisecond)
}

func TestMarkProcessed(t *testing.T) {
	t.Parallel()
	require.NoError(t, MarkProcessed(nil))
	inner := statusError(http.StatusServiceUnavailable)
	marked := MarkProcessed(fmt.Errorf("wrapped: %w", inner))
	assert.False(t, IsUnprocessedRequestError(marked))
	assert.Equal(t, http.StatusServiceUnavailable, HTTPStatusCode(marked), "status stays readable through the marker")
	assert.ErrorIs(t, marked, inner)
}

// An error that carries MarkProcessed ends the wait even when its cause is a
// coded MFA refusal: the operation got past its submission, and another attempt
// would run it again. The same holds for the first error, which must not start
// a wait or a retry.
func TestHandleCommonErrors_ProcessedErrorWithMFACodeStartsNoRetry(t *testing.T) {
	withFastRetry(t)

	t.Run("first error", func(t *testing.T) {
		var retryCount atomic.Int32
		first := MarkProcessed(fmt.Errorf("share failed: %w", codedMFARefusal{}))

		result := HandleCommonErrors(first, "server1", ErrorHandlerCallbacks{
			OnMFARequired:  func(string) error { return nil },
			RetryOperation: func() error { retryCount.Add(1); return nil },
		})

		assert.Equal(t, first, result)
		assert.Zero(t, retryCount.Load())
	})

	t.Run("error during the wait", func(t *testing.T) {
		var retryCount atomic.Int32
		late := MarkProcessed(fmt.Errorf("status check: %w", codedMFARefusal{}))

		result := HandleCommonErrors(codedMFARefusal{}, "server1", ErrorHandlerCallbacks{
			OnMFARequired: func(string) error { return nil },
			RetryOperation: func() error {
				retryCount.Add(1)
				return late
			},
		})

		assert.Equal(t, late, result)
		assert.Equal(t, int32(1), retryCount.Load())
	})
}
