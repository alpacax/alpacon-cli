package mfa

import (
	"os"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrorCallbacks_UsernameRequiredWithoutATerminalPointsAtUsernameSet(t *testing.T) {
	// Replace stdin with a pipe so IsInteractiveShell() reports non-TTY; otherwise
	// running `go test` from a terminal would enter the prompt loop and hang.
	orig := os.Stdin
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() {
		os.Stdin = orig
		_ = r.Close()
		_ = w.Close()
	})
	os.Stdin = r
	cb := ErrorCallbacks(&client.AlpaconClient{}, func() error { return nil })

	err = cb.OnUsernameRequired()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "alpacon username set")
	assert.NotContains(t, err.Error(), "Please enter")
}
