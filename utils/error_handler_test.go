package utils

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"testing/synctest"
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
		result := HandleCommonErrors(MarkSubmission(err), "server1", ErrorHandlerCallbacks{})
		assert.Equal(t, err, result)
	})

	t.Run("callback succeeds with retry", func(t *testing.T) {
		err := errors.New(`{"code": "user_username_required", "source": ""}`)
		result := HandleCommonErrors(MarkSubmission(err), "server1", ErrorHandlerCallbacks{
			OnUsernameRequired: func() error { return nil },
			RetryOperation:     func() error { return nil },
		})
		assert.NoError(t, result)
	})

	t.Run("callback fails", func(t *testing.T) {
		err := errors.New(`{"code": "user_username_required", "source": ""}`)
		cbErr := errors.New("username prompt failed")
		result := HandleCommonErrors(MarkSubmission(err), "server1", ErrorHandlerCallbacks{
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

// codedTestError is a refusal as the API client returns it: the code is a typed
// field, not text to be parsed.
type codedTestError struct{ code string }

func (e codedTestError) Error() string       { return "refused: " + e.code }
func (e codedTestError) ErrorCode() string   { return e.code }
func (e codedTestError) ErrorSource() string { return "" }

// typedMFARefusal is the refusal a retried attempt gets while MFA is pending.
func typedMFARefusal() error { return codedTestError{code: AuthMFARequired} }

func TestHandleCommonErrors_MFA_RetriesUntilTheRequestPasses(t *testing.T) {
	withFastRetry(t)
	var retryCount atomic.Int32

	result := HandleCommonErrors(MarkSubmission(errors.New(mfaRefusal)), "server1", ErrorHandlerCallbacks{
		OnMFARequired: func(string) error { return nil },
		RetryOperation: func() error {
			if retryCount.Add(1) < 3 {
				return MarkSubmission(typedMFARefusal())
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

	result := HandleCommonErrors(MarkSubmission(errors.New(mfaRefusal)), "server1", ErrorHandlerCallbacks{
		OnMFARequired: func(string) error { return nil },
		RetryOperation: func() error {
			if retryCount.Add(1) == 1 {
				return MarkSubmission(typedMFARefusal())
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

	result := HandleCommonErrors(MarkSubmission(errors.New(mfaRefusal)), "server1", ErrorHandlerCallbacks{
		OnMFARequired: func(string) error { return nil },
		RetryOperation: func() error {
			retryCount.Add(1)
			return MarkSubmission(typedMFARefusal())
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

	result := HandleCommonErrors(MarkSubmission(err), "server1", ErrorHandlerCallbacks{
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

	result := HandleCommonErrors(MarkSubmission(errors.New(mfaRefusal)), "server1", ErrorHandlerCallbacks{
		OnMFARequired: func(string) error { return nil },
		RetryOperation: func() error {
			switch retryCount.Add(1) {
			case 1, 2:
				return MarkSubmission(statusError(http.StatusServiceUnavailable))
			case 3:
				return MarkSubmission(typedMFARefusal())
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

	result := HandleCommonErrors(MarkSubmission(errors.New(mfaRefusal)), "server1", ErrorHandlerCallbacks{
		OnMFARequired: func(string) error { return nil },
		RetryOperation: func() error {
			retryCount.Add(1)
			return MarkSubmission(statusError(http.StatusTooManyRequests))
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

	result := HandleCommonErrors(MarkSubmission(errors.New(mfaRefusal)), "server1", ErrorHandlerCallbacks{
		OnMFARequired: func(string) error { return nil },
		RetryOperation: func() error {
			retryCount.Add(1)
			return MarkSubmission(statusError(http.StatusBadGateway))
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
		{"dial failure", MarkSubmission(dial), true},
		{"wrapped dial failure", MarkSubmission(fmt.Errorf("submit: %w", dial)), true},
		{"read failure after sending", MarkSubmission(read), false},
		{"429", MarkSubmission(statusError(http.StatusTooManyRequests)), true},
		{"503", MarkSubmission(statusError(http.StatusServiceUnavailable)), true},
		{"502", MarkSubmission(statusError(http.StatusBadGateway)), false},
		{"504", MarkSubmission(statusError(http.StatusGatewayTimeout)), false},
		{"403", MarkSubmission(statusError(http.StatusForbidden)), false},
		{"plain error", MarkSubmission(errors.New("boom")), false},
		// Without the tag an error is a later request's: the operation already
		// took effect, whatever the status or dial failure says.
		{"untagged 503", statusError(http.StatusServiceUnavailable), false},
		{"untagged dial failure", dial, false},
		{"tag hidden by wrapping", fmt.Errorf("read output: %w", MarkSubmission(statusError(http.StatusServiceUnavailable))), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, IsUnprocessedRequestError(tc.err))
		})
	}
}

// A refusal that names auth_mfa_required only in its text, such as an agent's
// transfer report, is not the server's MFA refusal and ends the wait.
func TestHandleCommonErrors_MFA_UntypedRefusalEndsTheWait(t *testing.T) {
	withFastRetry(t)
	untyped := errors.New(mfaRefusal)
	var retryCount atomic.Int32

	result := HandleCommonErrors(MarkSubmission(errors.New(mfaRefusal)), "server1", ErrorHandlerCallbacks{
		OnMFARequired: func(string) error { return nil },
		RetryOperation: func() error {
			retryCount.Add(1)
			return untyped
		},
	})

	assert.Equal(t, untyped, result)
	assert.Equal(t, int32(1), retryCount.Load())
}

// An error from a request after the side-effecting one is untagged and ends the
// wait whatever it carries, a 503 or even a typed MFA refusal: the operation
// must not run again.
func TestHandleCommonErrors_MFA_UntaggedErrorEndsTheWait(t *testing.T) {
	withFastRetry(t)
	cases := []struct {
		name string
		err  error
	}{
		{"503", statusError(http.StatusServiceUnavailable)},
		{"typed MFA refusal", typedMFARefusal()},
		{"tag hidden by wrapping", fmt.Errorf("read output: %w", MarkSubmission(statusError(http.StatusServiceUnavailable)))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			late := fmt.Errorf("read output: %w", tc.err)
			var retryCount atomic.Int32

			result := HandleCommonErrors(MarkSubmission(errors.New(mfaRefusal)), "server1", ErrorHandlerCallbacks{
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
}

// An error that is not the side-effecting request's own is returned as is, with
// no callback run: the operation already took effect, even when the error reads
// as an MFA refusal.
func TestHandleCommonErrors_UntaggedErrorRunsNoCallback(t *testing.T) {
	t.Parallel()
	err := typedMFARefusal()
	var called atomic.Bool
	mark := func() { called.Store(true) }

	result := HandleCommonErrors(err, "server1", ErrorHandlerCallbacks{
		OnMFARequired:      func(string) error { mark(); return nil },
		OnUsernameRequired: func() error { mark(); return nil },
		RetryOperation:     func() error { mark(); return nil },
	})

	assert.Equal(t, err, result)
	assert.False(t, called.Load(), "no callback may run on an untagged error")
}

// The error HandleCommonErrors returns carries no tag, so an enclosing retry
// loop cannot take it for a refused request.
func TestHandleCommonErrors_ReturnsNoTag(t *testing.T) {
	t.Parallel()
	inner := statusError(http.StatusServiceUnavailable)

	result := HandleCommonErrors(MarkSubmission(inner), "server1", ErrorHandlerCallbacks{})

	assert.Equal(t, inner, result)
	assert.False(t, IsSubmissionError(result))
}

type retryAfterTestError struct {
	statusError
	after time.Duration
}

func (e retryAfterTestError) RetryAfter() time.Duration { return e.after }

// A throttled attempt that names a Retry-After holds the next one back that
// long, capped like any poll backoff, instead of retrying a second later.
func TestHandleCommonErrors_MFA_HonorsRetryAfter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		after   time.Duration
		wantGap time.Duration
	}{
		{"short", 5 * time.Second, 5 * time.Second},
		{"capped", time.Hour, time.Duration(PollMaxBackoffTick) * time.Second},
		{"none", 0, time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				var attempts []time.Time
				result := HandleCommonErrors(MarkSubmission(errors.New(mfaRefusal)), "server1", ErrorHandlerCallbacks{
					OnMFARequired: func(string) error { return nil },
					RetryOperation: func() error {
						attempts = append(attempts, time.Now())
						if len(attempts) == 1 {
							return MarkSubmission(retryAfterTestError{statusError(http.StatusTooManyRequests), tc.after})
						}
						return nil
					},
				})

				require.NoError(t, result)
				require.Len(t, attempts, 2)
				assert.Equal(t, tc.wantGap, attempts[1].Sub(attempts[0]))
			})
		})
	}
}

// No attempt starts past the deadline: a Retry-After that reaches beyond it
// ends the wait at once instead of sending one more request when it runs out.
func TestHandleCommonErrors_MFA_SendsNoAttemptPastTheDeadline(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		var last time.Time
		var attempts int
		result := HandleCommonErrors(MarkSubmission(errors.New(mfaRefusal)), "server1", ErrorHandlerCallbacks{
			OnMFARequired: func(string) error { return nil },
			RetryOperation: func() error {
				attempts++
				last = time.Now()
				if time.Since(start) >= maxRetryDuration-30*time.Second {
					return MarkSubmission(retryAfterTestError{statusError(http.StatusTooManyRequests), time.Minute})
				}
				return MarkSubmission(typedMFARefusal())
			},
		})

		require.ErrorContains(t, result, "MFA authentication timed out")
		// Attempts go once a second; the one at 150s is told to wait a minute,
		// which runs past the 3-minute deadline, so it is the last.
		assert.Equal(t, maxRetryDuration-30*time.Second, last.Sub(start))
		assert.Equal(t, 150, attempts)
		assert.Equal(t, last, time.Now(), "the wait ends without sitting out a gap nothing follows")
	})
}

// A multi-step operation behind a retry callback that tags nothing must never
// be replayed: whatever its later requests answer, a transient 503 included,
// the wait does not take it for a refused submission.
func TestHandleCommonErrors_UntaggedOperationIsNeverReplayed(t *testing.T) {
	withFastRetry(t)
	var retryCount atomic.Int32

	result := HandleCommonErrors(typedMFARefusal(), "server1", ErrorHandlerCallbacks{
		OnMFARequired: func(string) error { return nil },
		RetryOperation: func() error {
			retryCount.Add(1)
			return MarkSubmission(statusError(http.StatusServiceUnavailable))
		},
	})

	require.Error(t, result)
	assert.Zero(t, retryCount.Load(), "an operation that tagged no request must not be retried at all")
}
