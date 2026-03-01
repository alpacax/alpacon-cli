package utils

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func init() {
	// Override retry interval to keep MFA tests fast
	retryInterval = 10 * time.Millisecond
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

	assert.ErrorContains(t, result, "failed to refresh token")
	assert.ErrorIs(t, result, refreshErr)
}

func TestHandleCommonErrors_MFA_RefreshThenRetrySucceeds(t *testing.T) {
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
