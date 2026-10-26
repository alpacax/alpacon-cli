package event

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testReconnectBaseDelay keeps reconnect assertions off the production 1s backoff.
const testReconnectBaseDelay = 10 * time.Millisecond

func provisionURL(wsURL string) func() (*EventSessionResponse, error) {
	return func() (*EventSessionResponse, error) {
		return &EventSessionResponse{ID: "session", WebsocketURL: wsURL}, nil
	}
}

// recordCloses takes no lock: the tests using it call connectAndListen on their own goroutine.
func recordCloses(w *wsListener) *[]string {
	var closed []string
	w.closeUnopened = func(id string) { closed = append(closed, id) }
	return &closed
}

func TestWSListener_StartPanicsWithoutHandleFrame(t *testing.T) {
	t.Parallel()
	w := newProvisionedWSListener(nil, provisionURL(""), 0)

	assert.PanicsWithValue(t, "event: wsListener.handleFrame must be assigned before Start", func() {
		w.Start()
	})
}

func TestWSListener_StopIsIdempotent(t *testing.T) {
	t.Parallel()
	w := newProvisionedWSListener(nil, provisionURL(""), 0)

	w.Stop()
	w.Stop()
	w.Stop()
}

func TestWSListener_WaitConnected_Success(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newProvisionedWSListener(nil, provisionURL(""), 0)

		// Simulate connection after short delay
		go func() {
			time.Sleep(50 * time.Millisecond)
			close(w.connected)
		}()

		result := w.WaitConnected(2 * time.Second)
		assert.True(t, result, "should return true when connected")
	})
}

func TestWSListener_WaitConnected_Timeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newProvisionedWSListener(nil, provisionURL(""), 0)

		start := time.Now()
		result := w.WaitConnected(100 * time.Millisecond)
		elapsed := time.Since(start)

		assert.False(t, result, "should return false on timeout")
		assert.Equal(t, 100*time.Millisecond, elapsed, "the timeout must fire on the deadline, not later")
	})
}

func TestWSListener_NextReconnectDelay(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		delay time.Duration
		want  time.Duration
	}{
		{"base doubles", wsReconnectBaseDelay, 2 * time.Second},
		{"below cap doubles", 8 * time.Second, 16 * time.Second},
		{"overshoot clamps to cap", 16 * time.Second, wsReconnectMaxDelay},
		{"cap stays at cap", wsReconnectMaxDelay, wsReconnectMaxDelay},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, nextReconnectDelay(tt.delay))
		})
	}
}

func TestWSListener_ConnectAndListen_ResetsOnlyAfterStableLifetime(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		stableLifetime time.Duration
		want           bool
	}{
		{"connection outliving the base delay but not the stable lifetime keeps the backoff", time.Second, false},
		{"connection outliving the stable lifetime resets the backoff", time.Millisecond, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			upgrader := websocket.Upgrader{}
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				time.Sleep(3 * testReconnectBaseDelay)
				_ = conn.Close()
			}))
			defer ts.Close()

			w := newProvisionedWSListener(nil, provisionURL("ws"+strings.TrimPrefix(ts.URL, "http")), time.Second)
			w.handleFrame = func([]byte) {}
			w.reconnectBaseDelay = testReconnectBaseDelay
			w.stableLifetime = tt.stableLifetime

			assert.Equal(t, tt.want, w.connectAndListen())
		})
	}
}

func TestWSListener_ListenLoop_KeepsBackoffAcrossShortConnections(t *testing.T) {
	t.Parallel()
	const attempts = 5

	tests := []struct {
		name           string
		provisionDelay time.Duration
		hold           time.Duration
	}{
		{"instant drops", 0, 0},
		{"slow provision then instant drops", 3 * testReconnectBaseDelay, 0},
		{"drops after outliving the base delay", 0, 3 * testReconnectBaseDelay},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			upgrader := websocket.Upgrader{}
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				time.Sleep(tt.hold)
				_ = conn.Close()
			}))
			defer ts.Close()

			started := make(chan time.Time, attempts)
			wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
			w := newProvisionedWSListener(nil, func() (*EventSessionResponse, error) {
				select {
				case started <- time.Now():
				default:
				}
				time.Sleep(tt.provisionDelay)
				return &EventSessionResponse{ID: "session", WebsocketURL: wsURL}, nil
			}, time.Second)
			w.handleFrame = func([]byte) {}
			w.reconnectBaseDelay = testReconnectBaseDelay
			w.stableLifetime = time.Second
			w.Start()
			defer w.Stop()

			times := make([]time.Time, 0, attempts)
			for len(times) < attempts {
				select {
				case at := <-started:
					times = append(times, at)
				case <-time.After(5 * time.Second):
					t.Fatalf("expected %d dial attempts, got %d", attempts, len(times))
				}
			}

			// A reset after every drop would hold each wait at one base delay.
			delay := testReconnectBaseDelay
			for i := 1; i < attempts; i++ {
				assert.GreaterOrEqual(t, times[i].Sub(times[i-1]), tt.provisionDelay+tt.hold+delay, "gap before attempt %d", i+1)
				delay *= 2
			}
		})
	}
}

func TestWSListener_ListenLoop_ResetsBackoffAfterStableConnections(t *testing.T) {
	t.Parallel()
	const (
		attempts = 6
		hold     = 3 * testReconnectBaseDelay
	)
	upgrader := websocket.Upgrader{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		time.Sleep(hold)
		_ = conn.Close()
	}))
	defer ts.Close()

	started := make(chan time.Time, attempts)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	w := newProvisionedWSListener(nil, func() (*EventSessionResponse, error) {
		select {
		case started <- time.Now():
		default:
		}
		return &EventSessionResponse{ID: "session", WebsocketURL: wsURL}, nil
	}, time.Second)
	w.handleFrame = func([]byte) {}
	w.reconnectBaseDelay = testReconnectBaseDelay
	w.stableLifetime = time.Millisecond
	w.Start()
	defer w.Stop()

	times := make([]time.Time, 0, attempts)
	for len(times) < attempts {
		select {
		case at := <-started:
			times = append(times, at)
		case <-time.After(5 * time.Second):
			t.Fatalf("expected %d dial attempts, got %d", attempts, len(times))
		}
	}

	// Without the reset the last wait alone would be 16 base delays; with it, one.
	last := times[attempts-1].Sub(times[attempts-2])
	assert.Less(t, last, hold+8*testReconnectBaseDelay)
}

func TestWSListener_ConnectAndListen_ReturnsFalseOnFailedHandshake(t *testing.T) {
	t.Parallel()
	// Responds 200 instead of upgrading, so Dial fails with ErrBadHandshake.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	w := newProvisionedWSListener(nil, provisionURL("ws"+strings.TrimPrefix(server.URL, "http")), time.Second)
	w.handleFrame = func([]byte) {}

	assert.False(t, w.connectAndListen(), "failed handshake should not count as connected")
	assert.False(t, w.WaitConnected(0), "connected must stay open after a failed dial")
}

func TestWSListener_ConnectAndListen_ReportsTheHandshakeStatus(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	w := newProvisionedWSListener(nil, provisionURL("ws"+strings.TrimPrefix(server.URL, "http")+"/ws/event/PATHTOKEN123/"), time.Second)
	var announced error
	w.onDialFailed = func(err error) { announced = err }

	w.connectAndListen()

	require.EqualError(t, announced, "failed to connect to the event channel: websocket: bad handshake (status 403 Forbidden)")
}

func TestWSListener_ListenLoop_DoesNotDialWhenAlreadyStopped(t *testing.T) {
	t.Parallel()
	var dialed atomic.Int32

	// Counted at handler entry so the increment happens-before Dial returns.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dialed.Add(1)
	}))
	defer server.Close()

	w := newProvisionedWSListener(nil, provisionURL("ws"+strings.TrimPrefix(server.URL, "http")), time.Second)
	w.handleFrame = func([]byte) {}
	w.Stop()

	returned := make(chan struct{})
	go func() {
		defer close(returned)
		w.listenLoop()
	}()

	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("listenLoop should return immediately when done is already closed")
	}

	assert.Zero(t, dialed.Load(), "listenLoop should not dial after Stop")
}

func TestWSListener_WaitConnected_Shutdown(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newProvisionedWSListener(nil, provisionURL(""), 0)

		go func() {
			time.Sleep(50 * time.Millisecond)
			close(w.done)
		}()

		start := time.Now()
		result := w.WaitConnected(5 * time.Second)
		elapsed := time.Since(start)

		assert.False(t, result, "should return false when done is closed")
		assert.Equal(t, 50*time.Millisecond, elapsed, "the stop must land on the close, not a tick later")
	})
}

func TestWSListener_ProvisionCalledPerDialAttempt(t *testing.T) {
	t.Parallel()
	upgrader := websocket.Upgrader{}
	var conns atomic.Int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		if conns.Add(1) == 1 {
			// Drop the first connection so the listener has to dial again.
			_ = conn.Close()
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer ts.Close()

	var provisions atomic.Int32
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")

	w := newProvisionedWSListener(nil, func() (*EventSessionResponse, error) {
		provisions.Add(1)
		return &EventSessionResponse{ID: "session", WebsocketURL: wsURL}, nil
	}, time.Second)
	w.handleFrame = func([]byte) {}
	w.reconnectBaseDelay = testReconnectBaseDelay
	w.Start()
	defer w.Stop()

	require.True(t, w.WaitConnected(2*time.Second))

	// The listener reconnects on its own; wait for the second dial.
	deadline := time.After(5 * time.Second)
	for conns.Load() < 2 {
		select {
		case <-deadline:
			t.Fatalf("expected a reconnect, conns=%d provisions=%d", conns.Load(), provisions.Load())
		case <-time.After(10 * time.Millisecond):
		}
	}

	assert.GreaterOrEqual(t, provisions.Load(), int32(2), "provision must run once per dial attempt")
}

func TestWSListener_ProvisionErrorIsAFailedAttempt(t *testing.T) {
	t.Parallel()
	var provisions atomic.Int32

	w := newProvisionedWSListener(nil, func() (*EventSessionResponse, error) {
		provisions.Add(1)
		return nil, errors.New("provision failed")
	}, time.Second)
	w.handleFrame = func([]byte) {}

	assert.False(t, w.connectAndListen(), "a provision error must not count as connected")
	assert.Equal(t, int32(1), provisions.Load())
	assert.False(t, w.WaitConnected(0), "connected must stay open when provisioning fails")
}

func TestWSListener_OnConnectedRunsBeforeConnectedCloses(t *testing.T) {
	t.Parallel()
	upgrader := websocket.Upgrader{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer ts.Close()

	// Atomics because the hook runs on the listener goroutine.
	var hookRan, openDuringHook atomic.Bool

	w := newProvisionedWSListener(nil, provisionURL("ws"+strings.TrimPrefix(ts.URL, "http")), time.Second)
	w.handleFrame = func([]byte) {}
	w.onConnected = func() error {
		hookRan.Store(true)
		openDuringHook.Store(!w.WaitConnected(0))
		return nil
	}
	w.Start()
	defer w.Stop()

	require.True(t, w.WaitConnected(2*time.Second))
	assert.True(t, hookRan.Load(), "onConnected must run on a successful dial")
	assert.True(t, openDuringHook.Load(), "connected must not close before onConnected returns")
}

func TestWSListener_OnConnectedErrorIsAFailedAttempt(t *testing.T) {
	t.Parallel()
	upgrader := websocket.Upgrader{}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer ts.Close()

	w := newProvisionedWSListener(nil, provisionURL("ws"+strings.TrimPrefix(ts.URL, "http")), time.Second)
	w.handleFrame = func([]byte) {}
	w.onConnected = func() error { return errors.New("subscribe rejected") }

	assert.False(t, w.connectAndListen(), "an onConnected error must not count as connected")
	assert.False(t, w.WaitConnected(0), "connected must stay open when onConnected fails")
}

// No connection opened the channel, so the server will never close this session itself.
func TestWSListener_ClosesTheSessionWhenTheDialFails(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})) // a 200, so the handshake fails
	defer server.Close()
	w := newProvisionedWSListener(nil, func() (*EventSessionResponse, error) {
		return &EventSessionResponse{ID: "session-1", WebsocketURL: "ws" + strings.TrimPrefix(server.URL, "http")}, nil
	}, time.Second)
	w.handleFrame = func([]byte) {}
	closed := recordCloses(w)

	w.connectAndListen()

	assert.Equal(t, []string{"session-1"}, *closed)
}

func TestWSListener_ClosesEachFailedDialsOwnSession(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})) // a 200, so the handshake fails
	defer server.Close()
	var provisions atomic.Int32
	w := newProvisionedWSListener(nil, func() (*EventSessionResponse, error) {
		n := provisions.Add(1)
		return &EventSessionResponse{ID: "session-" + strconv.Itoa(int(n)), WebsocketURL: "ws" + strings.TrimPrefix(server.URL, "http")}, nil
	}, time.Second)
	w.handleFrame = func([]byte) {}
	closed := recordCloses(w)

	w.connectAndListen()
	w.connectAndListen()

	assert.Equal(t, []string{"session-1", "session-2"}, *closed)
}

// The server closes a session when the connection that opened its channel drops.
func TestWSListener_LeavesAnOpenedSessionToTheServer(t *testing.T) {
	t.Parallel()
	upgrader := websocket.Upgrader{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		_ = conn.Close()
	}))
	defer ts.Close()
	w := newProvisionedWSListener(nil, provisionURL("ws"+strings.TrimPrefix(ts.URL, "http")), time.Second)
	w.handleFrame = func([]byte) {}
	closed := recordCloses(w)

	w.connectAndListen()

	assert.Empty(t, *closed)
}

func TestWSListener_LeavesTheSessionToTheServerWhenOnConnectedFails(t *testing.T) {
	t.Parallel()
	upgrader := websocket.Upgrader{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer ts.Close()
	w := newProvisionedWSListener(nil, provisionURL("ws"+strings.TrimPrefix(ts.URL, "http")), time.Second)
	w.handleFrame = func([]byte) {}
	w.onConnected = func() error { return errors.New("subscribe rejected") }
	closed := recordCloses(w)

	w.connectAndListen()

	assert.Empty(t, *closed)
}

func TestWSListener_ClosesNothingWhenProvisionFails(t *testing.T) {
	t.Parallel()
	w := newProvisionedWSListener(nil, func() (*EventSessionResponse, error) {
		return nil, errors.New("provision failed")
	}, time.Second)
	w.handleFrame = func([]byte) {}
	closed := recordCloses(w)

	w.connectAndListen()

	assert.Empty(t, *closed)
}

func TestWSListener_ClosesNothingForASessionWithoutAnID(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})) // a 200, so the handshake fails
	defer server.Close()
	w := newProvisionedWSListener(nil, func() (*EventSessionResponse, error) {
		return &EventSessionResponse{WebsocketURL: "ws" + strings.TrimPrefix(server.URL, "http")}, nil
	}, time.Second)
	w.handleFrame = func([]byte) {}
	closed := recordCloses(w)

	w.connectAndListen()

	assert.Empty(t, *closed)
}

func TestWSListener_SendsTheCloseToTheServerWhenTheDialFails(t *testing.T) {
	t.Parallel()
	deleted := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/", func(w http.ResponseWriter, r *http.Request) {}) // a 200, so the handshake fails
	mux.HandleFunc("DELETE "+eventSessionsURL+"{id}/", func(w http.ResponseWriter, r *http.Request) {
		deleted <- r.PathValue("id")
		w.WriteHeader(http.StatusNoContent)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	w := newProvisionedWSListener(ac, func() (*EventSessionResponse, error) {
		return &EventSessionResponse{ID: "session-1", WebsocketURL: "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/"}, nil
	}, time.Second)
	w.handleFrame = func([]byte) {}

	w.connectAndListen()

	select {
	case id := <-deleted:
		assert.Equal(t, "session-1", id)
	case <-time.After(2 * time.Second):
		t.Fatal("the failed dial's session was never closed")
	}
}

// Ctrl+C during an outage must not wait on a close the server is not answering.
func TestWSListener_StopDoesNotWaitForAPendingClose(t *testing.T) {
	t.Parallel()
	arrived := make(chan struct{}, 1)
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/", func(w http.ResponseWriter, r *http.Request) {}) // a 200, so the handshake fails
	mux.HandleFunc("DELETE "+eventSessionsURL+"{id}/", func(w http.ResponseWriter, r *http.Request) {
		select {
		case arrived <- struct{}{}:
		default:
		}
		<-release
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	defer close(release) // runs before ts.Close, which waits on the handler
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	w := newProvisionedWSListener(ac, provisionURL("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/"), time.Second)
	w.handleFrame = func([]byte) {}
	w.reconnectBaseDelay = testReconnectBaseDelay
	w.Start()
	select {
	case <-arrived:
	case <-time.After(2 * time.Second):
		t.Fatal("the failed dial's session was never closed")
	}

	w.Stop()

	select {
	case <-w.stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop waited on a close the server never answered")
	}
}

// Comparing function pointers, since ProxyFromEnvironment reads the environment
// once per process and never proxies a loopback host.
func TestNewListenerDialer_FollowsTheSystemProxy(t *testing.T) {
	t.Parallel()
	dialer := newListenerDialer(3 * time.Second)

	require.NotNil(t, dialer.Proxy)
	assert.Equal(t, reflect.ValueOf(http.ProxyFromEnvironment).Pointer(), reflect.ValueOf(dialer.Proxy).Pointer())
	assert.Equal(t, 3*time.Second, dialer.HandshakeTimeout)
}
