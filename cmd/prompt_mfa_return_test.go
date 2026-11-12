package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommandsReturnMFAFailureWithoutRewording(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"exec submit", []string{"exec", "--detach", "my-server", "true"}},
		{"exec stream", []string{"exec", "my-server", "true"}},
		{"exec upload", []string{"exec", "--file", "/tmp/script.sh", "--file-from", "LOCAL", "--upload", "my-server"}},
		{"exec download", []string{"exec", "--file", "/tmp/script.sh", "--from-server", "my-server"}},
		{"cp upload", []string{"cp", "LOCAL", "my-server:/tmp/script"}},
		{"cp download", []string{"cp", "my-server:/tmp/script", "DEST"}},
		{"edit download", []string{"edit", "--force", "my-server:/tmp/script"}},
		{"tunnel", []string{"tunnel", "my-server", "--local", "0", "--remote", "22"}},
		{"disruptive server action", []string{"agent", "restart", "my-server", "-y"}},
		{"server action", []string{"agent", "install-pam", "my-server", "-y"}},
		{"role grant", []string{"user", "role", "grant", "alice", "admin", "-y"}},
		{"role revoke", []string{"user", "role", "revoke", "alice", "admin", "--reason", "test", "-y"}},
		{"api", []string{"api", "/api/test/"}},
	}
	for _, tc := range cases {
		for _, format := range []string{utils.OutputFormatTable, utils.OutputFormatJSON} {
			t.Run(tc.name+" "+format, func(t *testing.T) {
				home := t.TempDir()
				t.Setenv(homeEnvVar(), home)
				t.Setenv("ALPACON_WORK_SESSION", "")
				local := filepath.Join(home, "script.sh")
				require.NoError(t, os.WriteFile(local, []byte("echo test"), 0600))
				linkRequests := 0
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch {
					case strings.HasPrefix(r.URL.Path, "/api/auth0/mfa"):
						linkRequests++
						w.WriteHeader(http.StatusInternalServerError)
						_, _ = fmt.Fprint(w, `{"detail":"step-up unavailable"}`)
					case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
						_, _ = fmt.Fprintf(w, `{"count":1,"results":[{"id":%q,"name":"my-server"}]}`, exitUserUUID)
					case r.Method == http.MethodGet && r.URL.Path == "/api/rbac/roles/":
						_, _ = fmt.Fprintf(w, `{"count":1,"results":[{"id":%q,"name":"admin"}]}`, exitUserUUID)
					case r.Method == http.MethodGet && r.URL.Path == "/api/iam/users/":
						_, _ = fmt.Fprintf(w, `{"count":1,"results":[{"id":%q,"username":"alice"}]}`, exitUserUUID)
					case r.Method == http.MethodGet && r.URL.Path == "/api/rbac/user-roles/":
						if tc.name == "role grant" {
							_, _ = fmt.Fprint(w, `{"count":0,"results":[]}`)
						} else {
							_, _ = fmt.Fprintf(w, `{"count":1,"results":[{"id":%q,"role":{"id":%q,"name":"admin"},"user":{"id":%q,"username":"alice"}}]}`, exitUserUUID, exitUserUUID, exitUserUUID)
						}
					default:
						w.WriteHeader(http.StatusForbidden)
						_, _ = fmt.Fprint(w, `{"code":"auth_mfa_required"}`)
					}
				}))
				defer ts.Close()
				require.NoError(t, config.CreateConfig(ts.URL, "demo", "test", "", "", "", "", 0, false))
				args := append([]string{"--output", format}, tc.args...)
				for i, arg := range args {
					if arg == "LOCAL" {
						args[i] = local
					}
					if arg == "DEST" {
						args[i] = filepath.Join(home, "out")
					}
				}
				saved := utils.OutputFormat
				t.Cleanup(func() { utils.OutputFormat = saved })
				var err error
				stdout, _ := testutil.CaptureOutput(t, func() { err = executeRootCommand(t, args) })
				var exitErr *utils.ExitError
				require.ErrorAs(t, err, &exitErr)
				assert.Equal(t, 1, exitErr.Code)
				require.EqualError(t, err, "MFA authentication failed: failed to get the MFA URL: step-up unavailable")
				assert.Equal(t, 1, linkRequests)
				assert.Empty(t, stdout)
				var printed bytes.Buffer
				utils.FprintExitError(&printed, exitErr)
				if format == utils.OutputFormatJSON {
					assert.JSONEq(t, `{"ok":false,"exit_code":1,"error_code":"http_500","message":"MFA authentication failed: failed to get the MFA URL: step-up unavailable","context":{"operation":""},"http_status":500,"server":{"detail":"step-up unavailable"}}`, printed.String())
				} else {
					assert.Contains(t, printed.String(), "Error: MFA authentication failed:")
				}
			})
		}
	}
}

func TestLoginDefaultPromptsReturnTheirFailure(t *testing.T) {
	for _, cfg := range []config.Config{{WorkspaceURL: "https://self.example.test"}, {WorkspaceURL: "https://demo.us1.alpacon.io", WorkspaceName: "demo"}, {}} {
		t.Run(cfg.WorkspaceURL+" default", func(t *testing.T) {
			cause := utils.NewExitError("No input received.")
			_, _, _, err := promptForLoginTargetWithPrompts(cfg, func(string, string) (string, error) { return "", cause }, func(string) (string, error) { return "demo", nil })
			require.ErrorIs(t, err, cause)
			var exitErr *utils.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, 1, exitErr.Code)
		})
	}
}
