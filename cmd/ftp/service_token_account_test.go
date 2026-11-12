package ftp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/testutil"
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
			t.Setenv("HOME", home)
			t.Setenv("ALPACON_WORK_SESSION", "")
			previous := utils.OutputFormat
			utils.OutputFormat = format
			t.Cleanup(func() { utils.OutputFormat = previous })

			stdout, _ := testutil.CaptureOutput(t, func() {
				err = CpCmd.RunE(CpCmd, []string{source, "root@my-server:/tmp/"})
			})

			var exitErr *utils.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, 1, exitErr.Code)
			var stderr bytes.Buffer
			utils.FprintExitError(&stderr, exitErr)
			assert.Empty(t, stdout)
			assert.True(t, submitted.Load(), "the upload creation must be refused")
			assert.Contains(t, stderr.String(), "the service token's application cannot run as that account on that server—run as an account bound to the application or as its own account, and check that it is provisioned there")
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
