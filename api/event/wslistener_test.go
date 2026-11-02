package event

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
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
	const hold = 3 * wsReconnectBaseDelay // outlives the base delay
	tests := []struct {
		name           string
		stableLifetime time.Duration
		want           bool
	}{
		{"connection shorter than the stable lifetime keeps the backoff", wsStableConnectionLifetime, false},
		{"connection lasting exactly the stable lifetime keeps the backoff", hold, false},
		{"connection outliving the stable lifetime resets the backoff", hold - time.Nanosecond, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				upgrader := websocket.Upgrader{}
				ts := testutil.NewPipeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					conn, err := upgrader.Upgrade(w, r, nil)
					if err != nil {
						return
					}
					time.Sleep(hold)
					_ = conn.Close()
				}))

				w := newProvisionedWSListener(nil, provisionURL(ts.WebsocketURL("/")), time.Second)
				w.transport = ts.Transport()
				w.handleFrame = func([]byte) {}
				w.stableLifetime = tt.stableLifetime

				assert.Equal(t, tt.want, w.connectAndListen())
			})
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
		{"slow provision then instant drops", 3 * wsReconnectBaseDelay, 0},
		{"drops after outliving the base delay", 0, 3 * wsReconnectBaseDelay},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				over := make(chan struct{}) // frees a handler still holding when the test ends
				defer close(over)
				upgrader := websocket.Upgrader{}
				ts := testutil.NewPipeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					conn, err := upgrader.Upgrade(w, r, nil)
					if err != nil {
						return
					}
					defer func() { _ = conn.Close() }()
					select {
					case <-time.After(tt.hold):
					case <-over:
					}
				}))

				started := make(chan time.Time, attempts)
				w := newProvisionedWSListener(nil, func() (*EventSessionResponse, error) {
					select {
					case started <- time.Now():
					default:
					}
					time.Sleep(tt.provisionDelay)
					return &EventSessionResponse{ID: "session", WebsocketURL: ts.WebsocketURL("/")}, nil
				}, time.Second)
				w.transport = ts.Transport()
				w.handleFrame = func([]byte) {}
				w.Start()
				defer func() { // a provision sleeping past the last attempt must end inside the bubble
					w.Stop()
					<-w.stopped
				}()

				times := make([]time.Time, 0, attempts)
				for len(times) < attempts {
					select {
					case at := <-started:
						times = append(times, at)
					case <-time.After(time.Minute):
						t.Fatalf("expected %d dial attempts, got %d", attempts, len(times))
					}
				}

				// A reset after every drop would hold each wait at one base delay.
				delay := wsReconnectBaseDelay
				for i := 1; i < attempts; i++ {
					assert.Equal(t, tt.provisionDelay+tt.hold+delay, times[i].Sub(times[i-1]), "gap before attempt %d", i+1)
					delay *= 2
				}
			})
		})
	}
}

func TestWSListener_ListenLoop_ResetsBackoffAfterStableConnections(t *testing.T) {
	t.Parallel()
	const (
		attempts = 6
		hold     = 3 * wsReconnectBaseDelay
	)
	synctest.Test(t, func(t *testing.T) {
		over := make(chan struct{}) // frees a handler still holding when the test ends
		defer close(over)
		upgrader := websocket.Upgrader{}
		ts := testutil.NewPipeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer func() { _ = conn.Close() }()
			select {
			case <-time.After(hold):
			case <-over:
			}
		}))

		started := make(chan time.Time, attempts)
		w := newProvisionedWSListener(nil, func() (*EventSessionResponse, error) {
			select {
			case started <- time.Now():
			default:
			}
			return &EventSessionResponse{ID: "session", WebsocketURL: ts.WebsocketURL("/")}, nil
		}, time.Second)
		w.transport = ts.Transport()
		w.handleFrame = func([]byte) {}
		w.stableLifetime = time.Millisecond
		w.Start()
		defer w.Stop()

		times := make([]time.Time, 0, attempts)
		for len(times) < attempts {
			select {
			case at := <-started:
				times = append(times, at)
			case <-time.After(time.Minute):
				t.Fatalf("expected %d dial attempts, got %d", attempts, len(times))
			}
		}

		// Without the reset each wait would double: 1s, 2s, 4s and on.
		for i := 1; i < attempts; i++ {
			assert.Equal(t, hold+wsReconnectBaseDelay, times[i].Sub(times[i-1]), "gap before attempt %d", i+1)
		}
	})
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
	synctest.Test(t, func(t *testing.T) {
		var dialed atomic.Int32
		ts := testutil.NewPipeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			dialed.Add(1)
		}))

		w := newProvisionedWSListener(nil, provisionURL(ts.WebsocketURL("/")), time.Second)
		w.transport = ts.Transport()
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
	})
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
	synctest.Test(t, func(t *testing.T) {
		upgrader := websocket.Upgrader{}
		var conns atomic.Int32

		ts := testutil.NewPipeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer func() { _ = conn.Close() }()
			if conns.Add(1) == 1 {
				return // drop the first connection so the listener has to dial again
			}
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}))

		var provisions atomic.Int32
		w := newProvisionedWSListener(nil, func() (*EventSessionResponse, error) {
			provisions.Add(1)
			return &EventSessionResponse{ID: "session", WebsocketURL: ts.WebsocketURL("/")}, nil
		}, time.Second)
		w.transport = ts.Transport()
		w.handleFrame = func([]byte) {}
		w.Start()
		defer w.Stop()

		require.True(t, w.WaitConnected(2*time.Second))
		time.Sleep(wsReconnectBaseDelay)
		synctest.Wait()

		assert.Equal(t, int32(2), conns.Load())
		assert.Equal(t, int32(2), provisions.Load(), "provision must run once per dial attempt")
	})
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
	synctest.Test(t, func(t *testing.T) {
		upgrader := websocket.Upgrader{}
		ts := testutil.NewPipeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer func() { _ = conn.Close() }()
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}))

		// Atomics because the hook runs on the listener goroutine.
		var hookRan, openDuringHook atomic.Bool

		w := newProvisionedWSListener(nil, provisionURL(ts.WebsocketURL("/")), time.Second)
		w.transport = ts.Transport()
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
	})
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
	synctest.Test(t, func(t *testing.T) {
		deleted := make(chan string, 1)
		mux := http.NewServeMux()
		mux.HandleFunc("/ws/", func(w http.ResponseWriter, r *http.Request) {}) // a 200, so the handshake fails
		mux.HandleFunc("DELETE "+eventSessionsURL+"{id}/", func(w http.ResponseWriter, r *http.Request) {
			deleted <- r.PathValue("id")
			w.WriteHeader(http.StatusNoContent)
		})
		ts := testutil.NewPipeServer(t, mux)
		ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
		w := newProvisionedWSListener(ac, func() (*EventSessionResponse, error) {
			return &EventSessionResponse{ID: "session-1", WebsocketURL: ts.WebsocketURL("/ws/")}, nil
		}, time.Second)
		w.handleFrame = func([]byte) {}

		w.connectAndListen()

		select {
		case id := <-deleted:
			assert.Equal(t, "session-1", id)
		case <-time.After(2 * time.Second):
			t.Fatal("the failed dial's session was never closed")
		}
	})
}

// Ctrl+C during an outage must not wait on a close the server is not answering.
func TestWSListener_StopDoesNotWaitForAPendingClose(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
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
		ts := testutil.NewPipeServer(t, mux)
		defer close(release) // runs before the server's cleanup, which waits on the handler
		ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
		w := newProvisionedWSListener(ac, provisionURL(ts.WebsocketURL("/ws/")), time.Second)
		w.handleFrame = func([]byte) {}
		w.Start()
		select {
		case <-arrived:
		case <-time.After(2 * time.Second):
			t.Fatal("the failed dial's session was never closed")
		}

		start := time.Now()
		w.Stop()

		select {
		case <-w.stopped:
		case <-time.After(time.Second):
			t.Fatal("Stop waited on a close the server never answered")
		}
		assert.Zero(t, time.Since(start), "the listener must stop without waiting")
	})
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

func TestWSListener_DialsTheRouteItsClientsTransportTakes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		proxy    func(*http.Request) (*url.URL, error)
		wantAddr string
	}{
		{"straight to the server without a proxy", nil, "pipe.test:80"},
		{"through the transport's proxy", http.ProxyURL(&url.URL{Scheme: "http", Host: "proxy.example:3128"}), "proxy.example:3128"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ps := testutil.NewPipeServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			dialed := make(chan string, 1)
			transport := &http.Transport{Proxy: tt.proxy, DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				select {
				case dialed <- addr:
				default:
				}
				return ps.DialContext(ctx, network, addr)
			}}
			ac := &client.AlpaconClient{HTTPClient: &http.Client{Transport: transport}, BaseURL: ps.URL}
			w := newProvisionedWSListener(ac, provisionURL(ps.WebsocketURL("/ws/")), time.Second)
			w.handleFrame = func([]byte) {}
			w.closeUnopened = func(string) {}

			w.connectAndListen()

			select {
			case addr := <-dialed:
				assert.Equal(t, tt.wantAddr, addr)
			default:
				t.Fatal("the listener never dialed through its client's transport")
			}
		})
	}
}

// httpclient.New sets both, but a transport may carry a proxy and leave the dial to the default.
func TestWSListener_TakesTheTransportsProxyWithoutItsDial(t *testing.T) {
	t.Parallel()
	errProxyConsulted := errors.New("proxy consulted")
	transport := &http.Transport{Proxy: func(*http.Request) (*url.URL, error) { return nil, errProxyConsulted }}
	ac := &client.AlpaconClient{HTTPClient: &http.Client{Transport: transport}, BaseURL: testutil.PipeServerURL}
	w := newProvisionedWSListener(ac, provisionURL("ws://pipe.test/ws/"), time.Second)
	w.handleFrame = func([]byte) {}
	w.closeUnopened = func(string) {}
	var dialErr error
	w.onDialFailed = func(err error) { dialErr = err }

	w.connectAndListen()

	require.ErrorIs(t, dialErr, errProxyConsulted)
}
