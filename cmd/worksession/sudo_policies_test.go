package worksession

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"

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

func TestBroadSudoPatterns(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		commands   []string
		authorized bool
		want       []string
	}{
		{"null commands", nil, true, []string{"all commands"}},
		{"empty commands", []string{}, true, []string{"all commands"}},
		{"wildcard", []string{"  *  "}, true, []string{"  *  "}},
		{"shell arguments and spacing", []string{"  /bin/bash   *  ", "sh -c *"}, true, []string{"  /bin/bash   *  ", "sh -c *"}},
		{"tab separators", []string{"bash\t-c\t*", "\t*\t"}, true, []string{"bash\t-c\t*", "\t*\t"}},
		{"similar names", []string{"bash-completion", "shellcheck *"}, true, nil},
		{"starred program", []string{"sh*", "*sh", "/bin/*", "* *"}, true, []string{"sh*", "*sh", "/bin/*", "* *"}},
		{"sudo with user", []string{"/usr/bin/sudo -u x *"}, true, []string{"/usr/bin/sudo -u x *"}},
		{"shells", []string{"bash", "sh", "zsh", "dash", "ksh", "fish", "csh", "tcsh"}, true, []string{"bash", "sh", "zsh", "dash", "ksh", "fish", "csh", "tcsh"}},
		{"switch user", []string{"sudo", "sudo ls", "/usr/bin/su - root"}, true, []string{"sudo", "sudo ls", "/usr/bin/su - root"}},
		{"bounded commands", []string{"systemctl restart nginx", "systemctl *", "/usr/bin/apt-get update", "", "  ", "bashful", "echo bash", "BASH"}, true, nil},
		{"ordinary policy", []string{"*", "bash", "sudo", "su"}, false, nil},
		{"ordinary all commands", nil, false, nil},
		{"mixed patterns", []string{"systemctl *", "/bin/sh -c *", "apt-get update", "su"}, true, []string{"/bin/sh -c *", "su"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, broadSudoPatterns([]wsapi.SudoPolicyInline{{Commands: tt.commands, SessionAuthorized: tt.authorized}}))
		})
	}
}

func TestWarnBroadSudoPolicies(t *testing.T) {
	original := utils.OutputFormat
	defer func() { utils.OutputFormat = original }()
	for _, format := range []string{"table", "json"} {
		t.Run(format, func(t *testing.T) {
			utils.OutputFormat = format
			stdout, stderr := testutil.CaptureOutput(t, func() {
				warnBroadSudoPolicies([]wsapi.SudoPolicyInline{{Commands: []string{"/bin/bash *", "sudo ls"}, SessionAuthorized: true}})
			})
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, "This allows any command as root. Session-authorized sudo runs matching commands without MFA or approval.")
			assert.Contains(t, stderr, `"/bin/bash *"`)
			assert.Contains(t, stderr, `"sudo ls"`)
		})
	}
	stdout, stderr := testutil.CaptureOutput(t, func() {
		warnBroadSudoPolicies([]wsapi.SudoPolicyInline{
			{Commands: []string{"*"}},
			{Commands: []string{"systemctl *"}, SessionAuthorized: true},
		})
	})
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

func TestSudoWarningCommandOutput(t *testing.T) {
	for _, operation := range []string{"create", "update"} {
		t.Run(operation, func(t *testing.T) {
			for _, format := range []string{"table", "json"} {
				t.Run(format, func(t *testing.T) {
					for _, pattern := range []string{"bash *", "systemctl restart nginx"} {
						t.Run(pattern, func(t *testing.T) {
							const id = "550e8400-e29b-41d4-a716-446655440000"
							session := wsapi.WorkSession{ID: id, Status: "active", Scopes: []string{"sudo"}, SudoPolicies: []wsapi.SudoPolicyInline{{Commands: []string{"/bin/sh *"}, SessionAuthorized: true}}}
							mutations := 0
							ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
								w.Header().Set("Content-Type", "application/json")
								switch {
								case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
									_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"my-server-id","name":"my-server"}]}`))
								case r.Method == http.MethodGet && r.URL.Path == "/api/work-sessions/sessions/"+id+"/":
									_ = json.NewEncoder(w).Encode(session)
								case r.Method == http.MethodPost && r.URL.Path == "/api/work-sessions/sessions/", r.Method == http.MethodPatch && r.URL.Path == "/api/work-sessions/sessions/"+id+"/":
									mutations++
									_ = json.NewEncoder(w).Encode(session)
								default:
									t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
									http.NotFound(w, r)
								}
							}))
							defer ts.Close()
							setupWorkSessionCommandConfig(t, ts.URL)
							oldFormat := utils.OutputFormat
							utils.OutputFormat = format
							t.Cleanup(func() { utils.OutputFormat = oldFormat })
							cmd := workSessionUpdateCmd
							args := []string{id}
							if operation == "create" {
								cmd = workSessionCreateCmd
								args = nil
								resetCreateCommandState(t)
							}
							resetWorkSessionFlags(cmd)
							t.Cleanup(func() { resetWorkSessionFlags(cmd) })
							flags := []string{"--sudo", pattern}
							if operation == "create" {
								flags = append(flags, "--purpose", "maintenance", "--scope", "command", "--server", "my-server", "--expires-in", "1h", "--requester-type", "user")
							}
							require.NoError(t, cmd.ParseFlags(flags))
							var runErr error
							stdout, stderr := testutil.CaptureOutput(t, func() { runErr = cmd.RunE(cmd, args) })
							require.NoError(t, runErr)
							assert.Equal(t, 1, mutations)
							if pattern == "bash *" {
								assert.Contains(t, stderr, "This allows any command as root.")
								assert.Contains(t, stderr, `"bash *"`)
							} else {
								assert.NotContains(t, stderr, "This allows any command as root.")
							}
							assert.NotContains(t, stderr, `"/bin/sh *"`)
							assert.NotContains(t, stdout, "This allows any command as root.")
							if format == "json" {
								var got map[string]any
								require.NoError(t, json.Unmarshal([]byte(stdout), &got))
								assert.Equal(t, true, got["ok"])
								assert.Equal(t, operation, got["operation"])
							}
						})
					}
				})
			}
		})
	}
}
