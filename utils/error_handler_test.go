package utils

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
	err := errors.New("connection refused")
	result := HandleCommonErrors(err, "server1", ErrorHandlerCallbacks{})
	assert.Equal(t, err, result)
}

func TestHandleCommonErrors_UsernameRequired(t *testing.T) {
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
	err := errors.New(`{"code": "auth_mfa_required", "source": "command"}`)
	result := HandleCommonErrors(err, "server1", ErrorHandlerCallbacks{})
	assert.Equal(t, err, result)
}

func TestHandleCommonErrors_MFA_RefreshTokenError(t *testing.T) {
	withFastRetry(t)
	err := errors.New(`{"code": "auth_mfa_required", "source": "command"}`)
	refreshErr := errors.New("refresh token expired")

	result := HandleCommonErrors(err, "server1", ErrorHandlerCallbacks{
		OnMFARequired: func(srv string) error { return nil },
		RefreshToken:  func() error { return refreshErr },
		RetryOperation: func() error {
			t.Error("RetryOperation should not be called when RefreshToken fails")
			return nil
		},
	})

	assert.ErrorContains(t, result, "failed to refresh token; please run 'alpacon login'")
	assert.ErrorIs(t, result, refreshErr)
}

func TestHandleCommonErrors_MFA_RefreshThenRetrySucceeds(t *testing.T) {
	withFastRetry(t)
	err := errors.New(`{"code": "auth_mfa_required", "source": "command"}`)
	var refreshCount atomic.Int32
	var retryCount atomic.Int32

	result := HandleCommonErrors(err, "server1", ErrorHandlerCallbacks{
		OnMFARequired: func(srv string) error { return nil },
		RefreshToken: func() error {
			refreshCount.Add(1)
			return nil
		},
		RetryOperation: func() error {
			retryCount.Add(1)
			return nil // succeed on first retry
		},
	})

	assert.NoError(t, result)
	assert.Equal(t, int32(1), refreshCount.Load(), "RefreshToken should be called once")
	assert.Equal(t, int32(1), retryCount.Load(), "RetryOperation should be called once")
}

func TestHandleCommonErrors_MFA_PollingSuccess(t *testing.T) {
	withFastRetry(t)
	err := errors.New(`{"code": "auth_mfa_required", "source": "command"}`)
	var pollCount atomic.Int32
	var refreshCount atomic.Int32
	var retryCount atomic.Int32

	result := HandleCommonErrors(err, "server1", ErrorHandlerCallbacks{
		OnMFARequired: func(srv string) error { return nil },
		CheckMFACompleted: func() (bool, error) {
			if pollCount.Add(1) >= 3 {
				return true, nil
			}
			return false, nil
		},
		RefreshToken: func() error {
			refreshCount.Add(1)
			return nil
		},
		RetryOperation: func() error {
			retryCount.Add(1)
			return nil
		},
	})

	assert.NoError(t, result)
	assert.GreaterOrEqual(t, pollCount.Load(), int32(3), "should poll until completed")
	assert.Equal(t, int32(1), refreshCount.Load(), "RefreshToken should be called once after completion")
	assert.Equal(t, int32(1), retryCount.Load(), "RetryOperation should be called once after completion")
}

func TestHandleCommonErrors_MFA_PollingErrorRecovery(t *testing.T) {
	withFastRetry(t)
	err := errors.New(`{"code": "auth_mfa_required", "source": "command"}`)
	var pollCount atomic.Int32

	result := HandleCommonErrors(err, "server1", ErrorHandlerCallbacks{
		OnMFARequired: func(srv string) error { return nil },
		CheckMFACompleted: func() (bool, error) {
			n := pollCount.Add(1)
			if n <= 2 {
				return false, errors.New("endpoint not found")
			}
			return true, nil
		},
		RefreshToken:   func() error { return nil },
		RetryOperation: func() error { return nil },
	})

	assert.NoError(t, result)
	assert.GreaterOrEqual(t, pollCount.Load(), int32(3), "should continue polling after errors")
}

func TestHandleCommonErrors_MFA_PollingThenRefreshFails(t *testing.T) {
	withFastRetry(t)
	err := errors.New(`{"code": "auth_mfa_required", "source": "command"}`)
	refreshErr := errors.New("refresh token expired")

	result := HandleCommonErrors(err, "server1", ErrorHandlerCallbacks{
		OnMFARequired:     func(srv string) error { return nil },
		CheckMFACompleted: func() (bool, error) { return true, nil },
		RefreshToken:      func() error { return refreshErr },
		RetryOperation: func() error {
			t.Error("RetryOperation should not be called when RefreshToken fails")
			return nil
		},
	})

	assert.ErrorContains(t, result, "failed to refresh token; please run 'alpacon login'")
	assert.ErrorIs(t, result, refreshErr)
}

func TestHandleCommonErrors_MFA_PollingThenRetryFails(t *testing.T) {
	withFastRetry(t)
	err := errors.New(`{"code": "auth_mfa_required", "source": "command"}`)
	retryErr := errors.New("session creation failed")

	result := HandleCommonErrors(err, "server1", ErrorHandlerCallbacks{
		OnMFARequired:     func(srv string) error { return nil },
		CheckMFACompleted: func() (bool, error) { return true, nil },
		RefreshToken:      func() error { return nil },
		RetryOperation:    func() error { return retryErr },
	})

	assert.Equal(t, retryErr, result)
}

func TestHandleCommonErrors_MFA_PollingTimeout(t *testing.T) {
	withFastRetry(t)
	withFastTimeout(t)
	err := errors.New(`{"code": "auth_mfa_required", "source": "command"}`)

	result := HandleCommonErrors(err, "server1", ErrorHandlerCallbacks{
		OnMFARequired:     func(srv string) error { return nil },
		CheckMFACompleted: func() (bool, error) { return false, nil },
		RefreshToken:      func() error { return nil },
		RetryOperation:    func() error { return nil },
	})

	assert.ErrorContains(t, result, "MFA authentication timed out")
}

func TestHandleCommonErrors_MFA_NilCheckMFACompleted_LegacyFlow(t *testing.T) {
	withFastRetry(t)
	err := errors.New(`{"code": "auth_mfa_required", "source": "command"}`)
	var retryCount atomic.Int32

	result := HandleCommonErrors(err, "server1", ErrorHandlerCallbacks{
		OnMFARequired: func(srv string) error { return nil },
		RefreshToken:  func() error { return nil },
		RetryOperation: func() error {
			retryCount.Add(1)
			return nil
		},
	})

	assert.NoError(t, result)
	assert.Equal(t, int32(1), retryCount.Load(), "legacy flow should still work")
}
