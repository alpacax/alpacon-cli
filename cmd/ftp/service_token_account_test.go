package ftp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCp_ApplicationAccountRefusal(t *testing.T) {
	for _, format := range []string{utils.OutputFormatTable, utils.OutputFormatJSON} {
		t.Run(format, func(t *testing.T) {
			var submitted atomic.Bool
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/servers/servers/":
					_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"server","name":"my-server"}]}`))
				case "/api/webftp/uploads/":
					submitted.Store(true)
					w.WriteHeader(400)
					_, _ = w.Write([]byte(`{"code":"application_system_user_not_found","detail":"Invalid input."}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer ts.Close()
			home := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(home, ".alpacon"), 0700))
			cfg, err := json.Marshal(map[string]any{"workspace_url": ts.URL, "workspace_name": "test", "token": "test-token"})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(home, ".alpacon", "config.json"), cfg, 0600))
			source := filepath.Join(home, "file.txt")
			require.NoError(t, os.WriteFile(source, []byte("test"), 0600))
			child := exec.Command(os.Args[0], "-test.run=^TestCpApplicationAccountHelper$")
			child.Env = append(os.Environ(), "ALPACON_CP_ACCOUNT_HELPER="+format, "ALPACON_CP_ACCOUNT_SOURCE="+source, "ALPACON_WORK_SESSION=", "HOME="+home)
			var stdout, stderr bytes.Buffer
			child.Stdout = &stdout
			child.Stderr = &stderr
			err = child.Run()
			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, 1, exitErr.ExitCode())
			assert.True(t, submitted.Load(), "the upload creation must be refused")
			assert.Empty(t, stdout.String())
			assert.Contains(t, stderr.String(), "application_system_user_not_found")
			assert.Contains(t, stderr.String(), "The service token's application cannot run as that account on that server. Run as an account bound to the application, or as its own account, and check that it is provisioned there (code: application_system_user_not_found).")
			if format == utils.OutputFormatTable {
				assert.NotContains(t, stderr.String(), "Invalid input.")
			}
			if format == utils.OutputFormatJSON {
				var envelope utils.JSONErrorEnvelope[map[string]any]
				lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
				require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &envelope))
				assert.Equal(t, utils.ApplicationSystemUserNotFound, envelope.ErrorCode)
				assert.NotContains(t, envelope.Message, "Invalid input.")
			}
		})
	}
}

func TestCpApplicationAccountHelper(t *testing.T) {
	format := os.Getenv("ALPACON_CP_ACCOUNT_HELPER")
	if format == "" {
		return
	}
	utils.OutputFormat = format
	CpCmd.Run(CpCmd, []string{os.Getenv("ALPACON_CP_ACCOUNT_SOURCE"), "root@my-server:/tmp/"})
}
