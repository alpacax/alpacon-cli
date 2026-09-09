package runtime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStartWithClientKeepsPinnedWorkspace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	bodies := make(chan map[string]any, 2)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"server-a"}]}`))
		case "/api/websh/tunnels/":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			bodies <- body
			w.WriteHeader(http.StatusBadRequest)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()
	ac := &client.AlpaconClient{BaseURL: ts.URL, HTTPClient: ts.Client(), WorkspaceName: "a"}
	require.NoError(t, config.CreateConfig("http://127.0.0.1:1", "b", "token", "", "", "", "", 0, false))
	for range 2 {
		_, err := StartWithClient(ac, StartOptions{ServerName: "server", LocalPort: "0", RemotePort: "22", WorkSessionID: "session-a"})
		require.ErrorContains(t, err, "failed to create tunnel session")
		select {
		case body := <-bodies:
			assert.Equal(t, "session-a", body["work_session"])
			assert.Equal(t, "server-a", body["server"])
		default:
			t.Fatalf("tunnel request did not reach the pinned client: %v", err)
		}
	}
}
