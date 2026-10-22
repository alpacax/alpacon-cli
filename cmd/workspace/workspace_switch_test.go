package workspace

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Serial: each case swaps os.Stderr through CaptureOutput.
func TestFetchKubernetesSurface(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		body          string
		expected      bool
		expectWarning string
	}{
		{name: "workspace exposes kubernetes", status: http.StatusOK, body: `{"auth0":{"method":"auth0"},"surfaces":{"kubernetes":true}}`, expected: true},
		{name: "workspace without kubernetes", status: http.StatusOK, body: `{"auth0":{"method":"auth0"},"surfaces":{"kubernetes":false}}`},
		{name: "older server omits surfaces", status: http.StatusOK, body: `{"auth0":{"method":"auth0"}}`},
		{name: "env unreachable reads as no", status: http.StatusInternalServerError, body: `{}`, expectWarning: "stays hidden until you run 'alpacon login'"},
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

			var got bool
			_, stderr := testutil.CaptureOutput(t, func() {
				got = fetchKubernetesSurface(ts.URL, "next", false)
			})

			assert.Equal(t, tt.expected, got)
			if tt.expectWarning != "" {
				assert.Contains(t, stderr, tt.expectWarning)
			} else {
				assert.Empty(t, stderr)
			}
		})
	}
}

// Serial: each case sets HOME.
func TestCommitSwitch(t *testing.T) {
	errUnreachable := errors.New("connection refused")
	tests := []struct {
		name              string
		newSurface        bool
		verifyErr         error
		expectURL         string
		expectSchemaName  string
		expectSurface     bool
		expectErrContains string
	}{
		{name: "switch records the new answer", newSurface: true, expectURL: "https://next.us1.alpacon.io", expectSchemaName: "next", expectSurface: true},
		{name: "switch to a workspace without kubernetes", expectURL: "https://next.us1.alpacon.io", expectSchemaName: "next"},
		{name: "failed connection restores the original answer", newSurface: false, verifyErr: errUnreachable,
			expectURL: "https://prev-slug.us1.alpacon.io", expectSchemaName: "prev", expectSurface: true,
			expectErrContains: `failed to connect to workspace "next": connection refused; reverted to "prev"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			require.NoError(t, config.CreateConfig("https://prev-slug.us1.alpacon.io", "prev-slug", "", "", "access-token", "refresh-token", "alpacon.io", 3600, false))
			require.NoError(t, config.SetWorkspaceEnv("https://prev-slug.us1.alpacon.io", "prev", true))
			orig, err := config.LoadConfig()
			require.NoError(t, err)

			err = commitSwitch(orig, "https://next.us1.alpacon.io", "next", tt.newSurface, func() error { return tt.verifyErr })
			if tt.expectErrContains != "" {
				require.ErrorIs(t, err, tt.verifyErr)
				assert.Contains(t, err.Error(), tt.expectErrContains)
			} else {
				require.NoError(t, err)
			}

			cfg, err := config.LoadConfig()
			require.NoError(t, err)
			assert.Equal(t, tt.expectURL, cfg.WorkspaceURL)
			assert.Equal(t, tt.expectSchemaName, cfg.WorkspaceIdentity())
			assert.Equal(t, tt.expectSurface, cfg.KubernetesSurface)
			assert.Equal(t, "access-token", cfg.AccessToken)
		})
	}
}

// Serial: each case sets HOME.
func TestCommitSwitch_FailedConnectionLeavesALoginMadeMeanwhile(t *testing.T) {
	tests := []struct {
		name         string
		anotherLogin string
		wantErr      error
	}{
		{name: "login elsewhere", anotherLogin: "https://other.zz9.alpacon.io", wantErr: config.ErrWorkspaceChanged},
		{name: "another account on the workspace switched to", anotherLogin: "https://next.us1.alpacon.io", wantErr: config.ErrLoginChanged},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			require.NoError(t, config.CreateConfig("https://prev.us1.alpacon.io", "prev", "", "", "access-token", "refresh-token", "alpacon.io", 3600, false))
			orig, err := config.LoadConfig()
			require.NoError(t, err)
			var want config.Config
			verify := func() error {
				// Another shell logs in while this one checks the connection.
				require.NoError(t, config.CreateConfig(tt.anotherLogin, "other", "", "", "other-access", "other-refresh", "alpacon.io", 3600, false))
				cfg, loadErr := config.LoadConfig()
				require.NoError(t, loadErr)
				want = cfg
				return errors.New("connection refused")
			}

			err = commitSwitch(orig, "https://next.us1.alpacon.io", "next", false, verify)

			require.ErrorIs(t, err, tt.wantErr)
			require.ErrorContains(t, err, `failed to connect to "next": connection refused; left the config as another login or switch set it since`)
			cfg, err := config.LoadConfig()
			require.NoError(t, err)
			assert.Equal(t, want, cfg)
		})
	}
}

func TestCommitSwitch_ConfigWriteFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	verified := false
	err := commitSwitch(config.Config{}, "https://next.us1.alpacon.io", "next", true, func() error { verified = true; return nil })

	require.ErrorContains(t, err, "failed to update config")
	assert.False(t, verified, "no connection check runs when the config could not be written")
}
