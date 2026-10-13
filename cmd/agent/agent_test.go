package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The command-tree tests share the package-global AgentCmd, which Cobra
// mutates lazily (sorting children, building flag sets), so they stay serial.
func TestAgentSubcommands(t *testing.T) {
	var names []string
	for _, c := range AgentCmd.Commands() {
		if c.Hidden {
			continue
		}
		names = append(names, c.Name())
	}
	assert.ElementsMatch(t, []string{"restart", "upgrade", "install-pam"}, names)
}

func TestAgentDisruptiveFlags(t *testing.T) {
	for _, name := range []string{"restart", "upgrade"} {
		t.Run(name, func(t *testing.T) {
			cmd, _, err := AgentCmd.Find([]string{name})
			require.NoError(t, err)
			yes := cmd.Flags().Lookup("yes")
			require.NotNil(t, yes)
			assert.Equal(t, "y", yes.Shorthand)
			assert.NotNil(t, cmd.Flags().Lookup("force"))
		})
	}
}

// install-pam is not disruptive: the server applies no busy guard to it, so
// the command offers no --force to override one.
func TestAgentInstallPAMFlags(t *testing.T) {
	cmd, _, err := AgentCmd.Find([]string{"install-pam"})
	require.NoError(t, err)
	yes := cmd.Flags().Lookup("yes")
	require.NotNil(t, yes)
	assert.Equal(t, "y", yes.Shorthand)
	assert.Nil(t, cmd.Flags().Lookup("force"))
}

// install-pam has to reach the server as the install_pam action with no force,
// which the command layer alone decides; the API test only covers the request.
func TestAgentInstallPAMSendsTheAction(t *testing.T) {
	const serverID = "pam-server-id"
	var body map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(api.ListResponse[server.ServerDetails]{
				Count:   1,
				Results: []server.ServerDetails{{ID: serverID, Name: "my-server"}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/servers/servers/"+serverID+"/actions/":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer ts.Close()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	require.NoError(t, config.CreateConfig(ts.URL, "test", "", "", "access-token", "refresh-token", "", 3600, false))

	cmd, _, err := AgentCmd.Find([]string{"install-pam"})
	require.NoError(t, err)
	require.NoError(t, cmd.Flags().Set("yes", "true"))
	t.Cleanup(func() { _ = cmd.Flags().Set("yes", "false") })

	cmd.Run(cmd, []string{"my-server"})

	assert.Equal(t, map[string]any{"action": "install_pam", "force": false}, body)
}
