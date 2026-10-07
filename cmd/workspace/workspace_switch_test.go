package workspace

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Serial: each case sets HOME and swaps os.Stderr through CaptureOutput.
func TestRefreshKubernetesSurface(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		body          string
		movedAway     bool
		expected      bool
		expectWarning string
	}{
		{name: "workspace exposes kubernetes", status: http.StatusOK, body: `{"auth0":{"method":"auth0"},"surfaces":{"kubernetes":true}}`, expected: true},
		{name: "workspace without kubernetes", status: http.StatusOK, body: `{"auth0":{"method":"auth0"},"surfaces":{"kubernetes":false}}`},
		{name: "older server omits surfaces", status: http.StatusOK, body: `{"auth0":{"method":"auth0"}}`},
		{name: "another shell switched away meanwhile", status: http.StatusOK, body: `{"auth0":{"method":"auth0"},"surfaces":{"kubernetes":true}}`, movedAway: true, expectWarning: "Did not save Kubernetes support for workspace"},
		{name: "env unreachable stays false", status: http.StatusInternalServerError, body: `{}`, expectWarning: "Could not check Kubernetes support on workspace"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/auth/env/" {
					t.Errorf("unexpected path %q", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer ts.Close()

			// Start where the switch leaves the config: the previous
			// workspace said yes, and SwitchWorkspace has cleared it.
			t.Setenv("HOME", t.TempDir())
			require.NoError(t, config.CreateConfig("https://prev.example.com", "prev", "", "", "access-token", "refresh-token", "alpacon.io", 3600, false))
			require.NoError(t, config.SetKubernetesSurface("https://prev.example.com", true))
			require.NoError(t, config.SwitchWorkspace(ts.URL, "next"))

			if tt.movedAway {
				require.NoError(t, config.SwitchWorkspace("https://elsewhere.example.com", "elsewhere"))
			}

			_, stderr := testutil.CaptureOutput(t, func() {
				refreshKubernetesSurface(ts.URL, "next", false)
			})

			cfg, err := config.LoadConfig()
			require.NoError(t, err)
			assert.Equal(t, tt.expected, cfg.KubernetesSurface)
			assert.Equal(t, "access-token", cfg.AccessToken)
			if tt.expectWarning != "" {
				assert.Contains(t, stderr, tt.expectWarning)
			} else {
				assert.Empty(t, stderr)
			}
		})
	}
}
