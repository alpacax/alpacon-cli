package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Serial: each case points HOME at a temp config and sets ALPACON_NO_BROWSER.
func TestRefreshServer_RetriesOnlyAnMFARefusal(t *testing.T) {
	tests := []struct {
		name          string
		firstStatus   int
		firstBody     string
		wantErrCode   string
		wantPosts     int32
		wantMFALookup int32
	}{
		{
			name:          "mfa refusal prompts and the retry succeeds",
			firstStatus:   http.StatusUnauthorized,
			firstBody:     `{"code": "auth_mfa_required"}`,
			wantPosts:     2,
			wantMFALookup: 1,
		},
		{
			name:          "other refusal is returned without a retry",
			firstStatus:   http.StatusForbidden,
			firstBody:     `{"code": "permission_denied"}`,
			wantErrCode:   "permission_denied",
			wantPosts:     1,
			wantMFALookup: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			var posts, mfaLookups atomic.Int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
					_, _ = w.Write([]byte(`{"count": 1, "results": [{"id": "550e8400-e29b-41d4-a716-446655440000", "name": "my-server"}]}`))
				case r.Method == http.MethodGet && r.URL.Path == "/api/auth0/mfa/":
					mfaLookups.Add(1)
					_, _ = w.Write([]byte(`{"mfa_url": "https://example.com/mfa"}`))
				case r.Method == http.MethodPost && r.URL.Path == "/api/servers/servers/550e8400-e29b-41d4-a716-446655440000/actions/":
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["action"] != "update_information" {
						t.Errorf("unexpected action body: %v (%v)", body, err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if posts.Add(1) == 1 {
						w.WriteHeader(tt.firstStatus)
						_, _ = w.Write([]byte(tt.firstBody))
						return
					}
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{}`))
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusInternalServerError)
				}
			}))
			t.Cleanup(ts.Close)

			t.Setenv("HOME", t.TempDir())
			t.Setenv("ALPACON_NO_BROWSER", "1")
			require.NoError(t, config.CreateConfig(ts.URL, "my-workspace", "api-token", "", "", "", "", 0, false))

			// When
			err := refreshServer("my-server")

			// Then
			if tt.wantErrCode == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				code, _ := utils.ParseErrorResponse(err)
				assert.Equal(t, tt.wantErrCode, code)
			}
			assert.Equal(t, tt.wantPosts, posts.Load())
			assert.Equal(t, tt.wantMFALookup, mfaLookups.Load())
		})
	}
}
