package event

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A malformed channel URL quotes its path token in the dial error that is handed
// to the outage announcer; the token must not reach it.
func TestListenerDialFailureOmitsThePathToken(t *testing.T) {
	t.Parallel()
	listener := newProvisionedWSListener(nil, func() (string, error) {
		return "wss://proxy.example.com/ws/event/sid/cid/PATHTOKEN123/%zz", nil
	}, time.Second)
	var announced error
	listener.onDialFailed = func(err error) { announced = err }

	listener.connectAndListen()

	require.Error(t, announced)
	assert.NotContains(t, announced.Error(), "PATHTOKEN123")
	assert.Contains(t, announced.Error(), "proxy.example.com", "the host is kept")
}
