package workspace

import (
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrorCallbacks_WiresEveryField(t *testing.T) {
	ac := &client.AlpaconClient{}
	retried := false

	cb := errorCallbacks(ac, func() error { retried = true; return nil })

	assert.NotNil(t, cb.OnMFARequired)
	// A nil CheckMFACompleted silently drops both update commands onto the
	// legacy retry loop in utils.HandleCommonErrors—no compile error, no failure.
	assert.NotNil(t, cb.CheckMFACompleted)
	assert.NotNil(t, cb.RefreshToken)
	// These commands take no username, unlike mfa.ErrorCallbacks.
	assert.Nil(t, cb.OnUsernameRequired)

	require.NotNil(t, cb.RetryOperation)
	assert.NoError(t, cb.RetryOperation())
	assert.True(t, retried, "RetryOperation must be the closure passed in")
}
