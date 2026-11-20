package worksession

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildSudoPolicies(t *testing.T) {
	t.Parallel()
	t.Run("one policy per --sudo value, commands split on comma", func(t *testing.T) {
		policies := buildSudoPolicies(
			[]string{"systemctl restart nginx,systemctl reload nginx", "tail -f /var/log/nginx/*.log"},
			"hotfix",
		)
		assert.Len(t, policies, 2)
		assert.Equal(t, []string{"systemctl restart nginx", "systemctl reload nginx"}, policies[0].Commands)
		assert.True(t, policies[0].SessionAuthorized)
		assert.Equal(t, "hotfix", policies[0].Reason)
		assert.Equal(t, []string{"tail -f /var/log/nginx/*.log"}, policies[1].Commands)
	})

	t.Run("empty and whitespace-only values are skipped", func(t *testing.T) {
		assert.Empty(t, buildSudoPolicies(nil, ""))
		assert.Empty(t, buildSudoPolicies([]string{"", " , "}, ""))
	})
}

func TestSudoWarningCommandOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		operation string
		format    string
		status    string
		fields    string
		want      string
	}{
		{"create active", "create", "json", "active", `"sudo_policies":[{"commands":["python3 *"],"session_authorized":true,"broad_patterns":["python3 *"]}]`, "python3 *"},
		{"create pending", "create", "table", "pending", `"sudo_policies":[],"proposed_sudo_policies":[{"commands":["*"],"session_authorized":true,"broad_patterns":["*"]}]`, "*"},
		{"create bounded", "create", "table", "active", `"sudo_policies":[{"commands":["sudo ls"],"session_authorized":true,"broad_patterns":[]}]`, ""},
		{"create legacy", "create", "json", "active", `"sudo_policies":[{"commands":["*"],"allow_bypass_mfa":true}]`, ""},
		{"update applied", "update", "table", "active", `"sudo_policies":[{"id":"existing","broad_patterns":["sh"]},{"id":"new","broad_patterns":["python3 *"]}]`, "python3 *"},
		{"update held", "update", "json", "active", `"sudo_policies":[{"id":"existing","broad_patterns":["sh"]}],"pending_modification_request":{"changes":{"sudo_policies":[{"id":"existing","broad_patterns":["sh"]},{"id":null,"broad_patterns":["python3 *"]}]}}`, "python3 *"},
		{"update with existing hold", "update", "json", "active", `"sudo_policies":[{"id":"new","broad_patterns":["python3 *"]}],"pending_modification_request":{"id":"held","changes":{"sudo_policies":[{"id":null,"broad_patterns":["old *"]}]}}`, "python3 *"},
		{"update legacy", "update", "json", "active", `"sudo_policies":[{"id":"existing","commands":["sh"]},{"id":"new","commands":["*"],"allow_bypass_mfa":true}]`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			const id = "550e8400-e29b-41d4-a716-446655440000"
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
					_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"my-server-id","name":"my-server"}]}`))
				case r.Method == http.MethodGet && r.URL.Path == "/api/work-sessions/sessions/"+id+"/":
					var pending string
					if tt.name == "update with existing hold" {
						pending = `,"pending_modification_request":{"id":"held","changes":{"sudo_policies":[{"id":null,"broad_patterns":["old *"]}]}}`
					}
					_, _ = fmt.Fprintf(w, `{"id":%q,"status":"active","scopes":["sudo"],"sudo_policies":[{"id":"existing","commands":["sh"],"session_authorized":true,"broad_patterns":["sh"]}]%s}`, id, pending)
				case r.Method == http.MethodPost && r.URL.Path == "/api/work-sessions/sessions/",
					r.Method == http.MethodPatch && r.URL.Path == "/api/work-sessions/sessions/"+id+"/":
					var request struct {
						SudoPolicies []map[string]json.RawMessage `json:"sudo_policies"`
					}
					assert.NoError(t, json.NewDecoder(r.Body).Decode(&request))
					for _, policy := range request.SudoPolicies {
						assert.NotContains(t, policy, "broad_patterns")
					}
					if tt.name == "update held" {
						w.WriteHeader(http.StatusAccepted)
					}
					_, _ = fmt.Fprintf(w, `{"id":%q,"status":%q,%s}`, id, tt.status, tt.fields)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer ts.Close()

			ios, stdout, stderr := utils.TestIOStreams()
			require.NoError(t, ios.SetOutputFormat(tt.format))
			newClient := cmdtest.ClientFactory(ts)
			var cmd *cobra.Command
			var args []string
			flags := []string{"--sudo", "python3 *"}
			if tt.operation == "create" {
				cmd = newWorkSessionCreateCmd(newClient, ios)
				flags = append(flags, "--purpose", "maintenance", "--scope", "command", "--server", "my-server", "--expires-in", "1h", "--requester-type", "agent")
			} else {
				cmd = newWorkSessionUpdateCmd(newClient, ios)
				args = []string{id}
			}
			require.NoError(t, cmd.ParseFlags(flags))
			err := cmd.RunE(cmd, args)
			if tt.status == "pending" {
				var exit *utils.ExitError
				require.ErrorAs(t, err, &exit)
				assert.Equal(t, utils.ExitCodePendingApproval, exit.Code)
			} else {
				require.NoError(t, err)
			}

			if tt.want != "" {
				assert.Contains(t, stderr.String(), fmt.Sprintf("%q", tt.want))
			} else {
				assert.NotContains(t, stderr.String(), "sudo patterns may allow arbitrary commands")
			}
			assert.NotContains(t, stderr.String(), `"sh"`)
			assert.NotContains(t, stderr.String(), `"old *"`)
			assert.NotContains(t, stdout.String(), "sudo patterns may allow arbitrary commands")
			if tt.format == "json" {
				var got struct {
					OK        bool   `json:"ok"`
					Operation string `json:"operation"`
				}
				require.NoError(t, json.Unmarshal(stdout.Bytes(), &got))
				assert.True(t, got.OK)
				assert.Equal(t, tt.operation, got.Operation)
			}
		})
	}
}
