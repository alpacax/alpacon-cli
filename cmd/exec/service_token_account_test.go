package exec

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExec_ApplicationAccountRefusal(t *testing.T) {
	for _, format := range []string{utils.OutputFormatTable, utils.OutputFormatJSON} {
		t.Run(format, func(t *testing.T) {
			var submitted atomic.Bool
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/servers/servers/":
					_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"server","name":"my-server"}]}`))
				case "/api/events/commands/":
					submitted.Store(true)
					w.WriteHeader(400)
					_, _ = w.Write([]byte(`{"code":"application_system_user_not_found","detail":"Invalid input."}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer ts.Close()
			stdout, stderr, exit := runExecHelper(t, ts.URL, "--output", format, "root@my-server", "--", "uptime")
			assert.Equal(t, 1, exit)
			assert.True(t, submitted.Load(), "the command submission must be refused")
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, "the service token's application cannot run as that account on that server—run as an account bound to the application or as its own account, and check that it is provisioned there")
			if format == utils.OutputFormatTable {
				assert.NotContains(t, stderr, "Invalid input.")
			}
			if format == utils.OutputFormatJSON {
				var envelope utils.JSONErrorEnvelope[map[string]any]
				require.NoError(t, json.Unmarshal([]byte(stderr), &envelope))
				assert.Equal(t, utils.ApplicationSystemUserNotFound, envelope.ErrorCode)
				assert.NotContains(t, envelope.Message, "Invalid input.")
			}
		})
	}
}
