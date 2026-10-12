package event

import (
	"bytes"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Once the MFA wait gets a submission accepted, the command has run. A read
// that fails after that, even with a 503, is the operation's result: the wait
// must not take it for a refused submission and run the command again.
func TestCommandStream_MFAWaitDoesNotResubmitAfterAnAcceptedCommand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cfg  streamingServerConfig
	}{
		{
			// The event session is refused, so the run polls the command it
			// submitted and then reads its output from the chunk endpoint.
			name: "polling fallback",
			cfg:  streamingServerConfig{sessionStatus: http.StatusForbidden},
		},
		{
			// The listener connects, but subscribing to the accepted command fails,
			// so the run falls back to polling the command it already submitted.
			name: "event listener",
			cfg:  streamingServerConfig{subscribeStatus: http.StatusServiceUnavailable},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var submits atomic.Int32
			cfg := tc.cfg
			cfg.cmdID = "cmd-uuid"
			cfg.serverID = "srv-uuid"
			cfg.submitRefusals = 1
			cfg.submits = &submits
			// Completed with nothing in result, so the output has to come from
			// the chunk endpoint, which is unavailable.
			cfg.terminal = EventDetails{Status: "completed", Success: boolPtr(true)}
			cfg.chunkStatus = http.StatusServiceUnavailable
			ac := newStreamingServers(t, cfg)

			stream := NewCommandStream(ac)
			defer stream.Close()
			var out bytes.Buffer
			run := func() error {
				return stream.RunCommand("srv", "echo hi", "", "", nil, "", "", &out)
			}

			first := run()
			code, _ := utils.ParseErrorResponse(first)
			require.Equal(t, utils.AuthMFARequired, code, "got %v", first)

			err := utils.HandleCommonErrors(first, "srv", utils.ErrorHandlerCallbacks{
				OnMFARequired:  func(string) error { return nil },
				RetryOperation: run,
			})

			require.ErrorContains(t, err, "failed to read command output")
			assert.Equal(t, int32(1), submits.Load()-int32(cfg.submitRefusals),
				"exactly one accepted submission: the command must not run again")
		})
	}
}
