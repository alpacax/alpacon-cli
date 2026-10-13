package worksession_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type endSessionFunc func(ac *client.AlpaconClient, id string) (bool, error)

var endSessionCommands = []struct {
	name   string
	action string
	run    endSessionFunc
}{
	{"complete", "complete", worksession.RunComplete},
	{"revoke", "revoke", worksession.RunRevoke},
}

func endSessionServer(t *testing.T, id, action string, status int) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/work-sessions/sessions/"+id+"/"+action+"/" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestEndingTheActiveSessionClearsIt(t *testing.T) {
	for _, tc := range endSessionCommands {
		t.Run(tc.name, func(t *testing.T) {
			setupTmpConfig(t)
			require.NoError(t, config.SetActiveWorkSession("ses-abc"))
			ac := newTestClient(endSessionServer(t, "ses-abc", tc.action, http.StatusOK))

			cleared, err := tc.run(ac, "ses-abc")

			require.NoError(t, err)
			assert.True(t, cleared)
			got, err := config.GetActiveWorkSession()
			require.NoError(t, err)
			assert.Empty(t, got)
		})
	}
}

func TestEndingAnotherSessionKeepsTheActiveOne(t *testing.T) {
	for _, tc := range endSessionCommands {
		t.Run(tc.name, func(t *testing.T) {
			setupTmpConfig(t)
			require.NoError(t, config.SetActiveWorkSession("ses-keep"))
			ac := newTestClient(endSessionServer(t, "ses-other", tc.action, http.StatusOK))

			cleared, err := tc.run(ac, "ses-other")

			require.NoError(t, err)
			assert.False(t, cleared)
			got, err := config.GetActiveWorkSession()
			require.NoError(t, err)
			assert.Equal(t, "ses-keep", got)
		})
	}
}

func TestFailingToEndTheActiveSessionKeepsIt(t *testing.T) {
	for _, tc := range endSessionCommands {
		t.Run(tc.name, func(t *testing.T) {
			setupTmpConfig(t)
			require.NoError(t, config.SetActiveWorkSession("ses-abc"))
			ac := newTestClient(endSessionServer(t, "ses-abc", tc.action, http.StatusForbidden))

			cleared, err := tc.run(ac, "ses-abc")

			require.Error(t, err)
			assert.False(t, cleared)
			got, err := config.GetActiveWorkSession()
			require.NoError(t, err)
			assert.Equal(t, "ses-abc", got)
		})
	}
}

func TestEndingTheActiveSessionWarnsWhenTheConfigCannotBeCleared(t *testing.T) {
	for _, tc := range endSessionCommands {
		t.Run(tc.name, func(t *testing.T) {
			setupTmpConfig(t)
			home, err := os.UserHomeDir()
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(home, config.ConfigFileDir, config.ConfigFileName), []byte("{"), 0600))
			ac := newTestClient(endSessionServer(t, "ses-abc", tc.action, http.StatusOK))

			var cleared bool
			_, stderr := testutil.CaptureOutput(t, func() {
				cleared, err = tc.run(ac, "ses-abc")
			})

			require.NoError(t, err)
			assert.False(t, cleared)
			assert.Contains(t, stderr, "Work session ses-abc has ended but is still the active work-session")
			assert.Contains(t, stderr, "Run 'alpacon work-session use --unset' to clear it.")
		})
	}
}
