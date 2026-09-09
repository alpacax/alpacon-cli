package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInlineCredentialNextActions(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"exec", []string{"exec", "prod", "--", "mysql", "-pSecret"}, `alpacon exec --env="SECRET_NAME" db-server -- <command>`},
		{"websh", []string{"websh", "prod", "mysql -pSecret"}, `alpacon websh --env="SECRET_NAME" db-server '<command>'`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/events/sessions/":
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"detail":"event session unavailable"}`))
				case "/api/servers/servers/":
					_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
				case "/api/events/commands/":
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"code":"command_inline_credential","detail":"mysql -pSecret"}`))
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer ts.Close()
			home := t.TempDir()
			dir := filepath.Join(home, ".alpacon")
			require.NoError(t, os.MkdirAll(dir, 0700))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "config.json"),
				[]byte(`{"workspace_url":"`+ts.URL+`","workspace_name":"test","token":"test-token"}`), 0600))

			stdout, stderr, code := runHelperProcess(t, "TestInlineCredentialHelperProcess", "credential-helper",
				append([]string{"--output", "json"}, tt.args...), "GO_WANT_CREDENTIAL_HELPER=1",
				homeEnvVar()+"="+home, "ALPACON_WORK_SESSION=")
			assert.Equal(t, 1, code)
			assert.Empty(t, stdout)
			assert.JSONEq(t, `{
				"ok":false,"exit_code":1,"error_code":"command_inline_credential",
				"message":"server rejected this command—the command line carries a credential.",
				"context":{"operation":"command"},
				"next_actions":[{"command":`+strconv.Quote(tt.want)+`}]
			}`, stderr)
			assert.NotContains(t, stderr, "-pSecret")
			assert.NotContains(t, stderr, "mysql")
		})
	}
}

func TestInlineCredentialHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_CREDENTIAL_HELPER") != "1" {
		return
	}
	args, ok := helperArgsAfter(os.Args, "credential-helper")
	require.True(t, ok)
	RootCmd.SetArgs(args)
	Execute()
}
