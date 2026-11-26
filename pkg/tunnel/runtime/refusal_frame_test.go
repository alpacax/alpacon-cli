package runtime

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	tunnelapi "github.com/alpacax/alpacon-cli/api/tunnel"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/localsignal"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tunnel reads text frames apart from smux, so it may ask the proxy for
// refusal frames.
func TestDialTunnelAdvertisesRefusalFrames(t *testing.T) {
	t.Parallel()
	headers := make(chan http.Header, 1)
	upgrader := websocket.Upgrader{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Clone()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			_ = conn.Close()
		}
	}))
	t.Cleanup(ts.Close)
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	session := &tunnelapi.TunnelSessionResponse{ID: "tun-1", WebsocketURL: "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/tunnel/channel-token/"}

	conn, err := dialTunnel(ac, session, "db-1", config.GetSmuxConfig().MaxFrameSize, time.Second)
	require.NoError(t, err)
	_ = conn.Close()

	assert.Equal(t, client.CapabilityRefusalFrame, (<-headers).Get(client.ClientCapabilitiesHeader))
}

// A paused refusal frame on the tunnel signals the paused session; any other
// text frame signals nothing.
func TestTunnelRefusalSignalsAPausedSession(t *testing.T) {
	got := make(chan localsignal.Fields, 2)
	original := emitPaused
	emitPaused = func(fields localsignal.Fields, _ *client.AlpaconClient) { got <- fields }
	t.Cleanup(func() { emitPaused = original })
	handle := tunnelRefusalHandler(&client.AlpaconClient{})

	handle([]byte(`{"type":"refusal","code":"session_paused","work_session_id":"11111111-1111-4111-8111-111111111111","console_url":"https://alpacon.io/acme/work-sessions/11111111-1111-4111-8111-111111111111"}`))
	handle([]byte(`{"type":"refusal","code":"other"}`))

	assert.Equal(t, localsignal.Fields{
		Session:    "11111111-1111-4111-8111-111111111111",
		ConsoleURL: "https://alpacon.io/acme/work-sessions/11111111-1111-4111-8111-111111111111",
	}, <-got)
	assert.Empty(t, got)
}

func TestTunnelRefusalCoalescesWhileNotificationIsBusy(t *testing.T) {
	original := emitPaused
	t.Cleanup(func() { emitPaused = original })
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		release := make(chan struct{})
		emitPaused = func(_ localsignal.Fields, _ *client.AlpaconClient) {
			calls.Add(1)
			<-release
		}
		handle := tunnelRefusalHandler(&client.AlpaconClient{})
		frame := []byte(`{"type":"refusal","code":"session_paused","work_session_id":"11111111-1111-4111-8111-111111111111"}`)
		read := make(chan struct{})
		go func() {
			for range 1000 {
				handle(frame)
			}
			close(read)
		}()
		synctest.Wait()
		select {
		case <-read:
		default:
			t.Error("notification blocked the tunnel reader")
		}
		assert.EqualValues(t, 1, calls.Load())
		time.Sleep(2 * time.Minute)
		go handle(frame)
		synctest.Wait()
		assert.EqualValues(t, 1, calls.Load())
		close(release)
		synctest.Wait()
		assert.EqualValues(t, 1, calls.Load())
		handle(frame)
		synctest.Wait()
		assert.EqualValues(t, 2, calls.Load())
		handle(frame)
		synctest.Wait()
		assert.EqualValues(t, 2, calls.Load())
	})
}
