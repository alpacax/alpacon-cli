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

const mfaRefusal = `{"code": "auth_mfa_required", "source": "command"}`

func TestHandleCommonErrors_MFA_RetriesUntilTheRequestPasses(t *testing.T) {
	withFastRetry(t)
	var retryCount atomic.Int32

	result := HandleCommonErrors(errors.New(mfaRefusal), "server1", ErrorHandlerCallbacks{
		OnMFARequired: func(string) error { return nil },
		RetryOperation: func() error {
			if retryCount.Add(1) < 3 {
				return errors.New(mfaRefusal)
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
				return errors.New(mfaRefusal)
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
			return errors.New(mfaRefusal)
		},
	})

	assert.ErrorContains(t, result, "MFA authentication timed out")
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
				return errors.New(mfaRefusal)
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
		assert.Equal(t, tc.want, IsUnprocessedRequestError(tc.err), tc.name)
	}
}
