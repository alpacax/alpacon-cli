package websh

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A websocket URL the server returned carries a channel token in its query; a
// dial error must not print it.
func TestDialErrorOmitsTheQuery(t *testing.T) {
	t.Parallel()
	err := newWebsocketClient(nil).dial("ws://[::1?token=secret123")

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secret123")
	assert.NotContains(t, err.Error(), "token=")
}
