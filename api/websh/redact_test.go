package websh

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The server puts the channel token in a path segment of the websocket URL, and
// a malformed URL makes the dial error quote all of it: the token must not print.
func TestDialErrorOmitsThePathToken(t *testing.T) {
	t.Parallel()
	err := newWebsocketClient(nil).dial("wss://proxy.example.com/ws/websh/sid/cid/PATHTOKEN123/%zz")

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "PATHTOKEN123")
	assert.Contains(t, err.Error(), "proxy.example.com", "the host is kept")
}
