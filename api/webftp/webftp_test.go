package webftp

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetWebFTPLogList(t *testing.T) {
	tests := []struct {
		name         string
		serverName   string
		userName     string
		action       string
		serverFound  bool
		userFound    bool
		wantErr      bool
		wantParams   map[string]string
		absentParams []string
	}{
		{
			name:         "resolves server name to id",
			serverName:   "web-editor",
			serverFound:  true,
			wantParams:   map[string]string{"server": "srv-uuid"},
			absentParams: []string{"server_name"},
		},
		{
			name:         "resolves user name to id",
			userName:     "some-user",
			userFound:    true,
			wantParams:   map[string]string{"user": "usr-uuid"},
			absentParams: []string{"user_name"},
		},
		{
			name:        "server not found returns error",
			serverName:  "ghost",
			serverFound: false,
			wantErr:     true,
		},
		{
			name:      "user not found returns error",
			userName:  "ghost",
			userFound: false,
			wantErr:   true,
		},
		{
			name:         "no name filter skips resolution",
			action:       "upload",
			wantParams:   map[string]string{"action": "upload"},
			absentParams: []string{"server", "user"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured url.Values
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/servers/servers/":
					if tt.serverFound {
						_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-uuid"}]}`))
					} else {
						_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
					}
				case "/api/iam/users/":
					if tt.userFound {
						_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"usr-uuid"}]}`))
					} else {
						_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
					}
				case "/api/history/webftp-logs/":
					captured = r.URL.Query()
					_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
				default:
					t.Errorf("unexpected request path: %s", r.URL.Path)
				}
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

			_, err := GetWebFTPLogList(ac, 25, tt.serverName, tt.userName, tt.action)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			for key, want := range tt.wantParams {
				assert.Equal(t, want, captured.Get(key))
			}
			for _, key := range tt.absentParams {
				assert.Empty(t, captured.Get(key))
			}
		})
	}
}
