package webftp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetWebFTPLogList(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		serverName      string
		userName        string
		action          string
		serverFound     bool
		userFound       bool
		wantErr         bool
		wantErrContains string
		wantParams      map[string]string
		absentParams    []string
	}{
		{
			name:         "resolves server name to id",
			serverName:   "web-editor",
			serverFound:  true,
			wantParams:   map[string]string{"server": "srv-uuid", "page_size": "25"},
			absentParams: []string{"server_name"},
		},
		{
			name:         "resolves user name to id",
			userName:     "some-user",
			userFound:    true,
			wantParams:   map[string]string{"user": "usr-uuid", "page_size": "25"},
			absentParams: []string{"user_name"},
		},
		{
			name:         "resolves server and user together",
			serverName:   "web-editor",
			userName:     "some-user",
			serverFound:  true,
			userFound:    true,
			wantParams:   map[string]string{"server": "srv-uuid", "user": "usr-uuid", "page_size": "25"},
			absentParams: []string{"server_name", "user_name"},
		},
		{
			name:            "server not found returns error",
			serverName:      "ghost",
			serverFound:     false,
			wantErr:         true,
			wantErrContains: `--server "ghost"`,
		},
		{
			name:            "user not found returns error",
			userName:        "ghost",
			userFound:       false,
			wantErr:         true,
			wantErrContains: `--user "ghost"`,
		},
		{
			name:         "no name filter skips resolution",
			action:       "upload",
			wantParams:   map[string]string{"action": "upload", "page_size": "25"},
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
					_, _ = w.Write([]byte(`{"next":"","results":[]}`))
				default:
					t.Errorf("unexpected request path: %s", r.URL.Path)
					http.Error(w, "unexpected request path", http.StatusNotFound)
				}
			}))
			defer ts.Close()

			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

			_, err := GetWebFTPLogList(ac, 25, tt.serverName, tt.userName, tt.action)
			if tt.wantErr {
				require.Error(t, err)
				if tt.wantErrContains != "" {
					assert.ErrorContains(t, err, tt.wantErrContains)
				}
				return
			}
			require.NoError(t, err)
			for key, want := range tt.wantParams {
				assert.Equal(t, want, captured.Get(key))
			}
			for _, key := range tt.absentParams {
				_, ok := captured[key]
				assert.False(t, ok)
			}
		})
	}
}

// Regression for #274: a cursor-token next crashed the old int-typed unmarshal.
func TestGetWebFTPLogList_FollowsCursor(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(w).Encode(api.CursorListResponse[WebFTPLogEntry]{
				Next:    "eyJzIjpbMV0sImQiOiJhZnRlciJ9",
				Results: []WebFTPLogEntry{{FileName: "a.txt", Action: "upload"}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(api.CursorListResponse[WebFTPLogEntry]{
			Results: []WebFTPLogEntry{{FileName: "b.txt", Action: "download"}},
		})
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	logs, err := GetWebFTPLogList(ac, 5, "", "", "")
	require.NoError(t, err)
	require.Len(t, logs, 2)
	assert.Equal(t, "b.txt", logs[1].FileName)
}
