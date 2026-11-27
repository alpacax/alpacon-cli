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

const accountRefusalSentence = "the server account or group is not valid on this server, or you may not use it—name one with -u USERNAME or -g GROUPNAME where the command takes them, or use a work session that names an account"

// Serial: runExecHelper sets HOME.
func TestExecAccountRefusalNamesTheRemedy(t *testing.T) {
	tests := []struct {
		name        string
		target      string
		wantTable   string
		wantJSON    string
		wantActions []utils.NextAction
	}{
		{
			name:      "named account takes the server sentence",
			target:    "bob@my-server",
			wantTable: "Error: failed to execute command on 'my-server' server: " + accountRefusalSentence + "\n",
			wantJSON:  "failed to execute command on 'my-server' server: " + accountRefusalSentence,
		},
		{
			name:   "no account keeps the execution user hint in the table",
			target: "my-server",
			wantTable: "Error: no execution user was resolved for 'my-server' server.\n" +
				"Hint: Specify an execution user with -u USERNAME (and optionally -g GROUP), or use USERNAME@SERVER.\n" +
				"  alpacon exec -u USERNAME SERVER -- COMMAND\n" +
				"  alpacon exec USERNAME@SERVER -- COMMAND\n",
			wantJSON:    "no execution user was resolved for 'my-server' server.",
			wantActions: []utils.NextAction{{Description: "Specify an execution user with -u USERNAME (and optionally -g GROUP), or use USERNAME@SERVER."}},
		},
	}
	for _, tt := range tests {
		for _, format := range []string{utils.OutputFormatTable, utils.OutputFormatJSON} {
			t.Run(tt.name+"/"+format, func(t *testing.T) {
				var submitted atomic.Int32
				ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/api/servers/servers/":
						_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47","name":"my-server"}]}`))
					case "/api/events/commands/":
						submitted.Add(1)
						w.WriteHeader(http.StatusBadRequest)
						_, _ = w.Write([]byte(`{"code":"server_username_groupname_invalid","field_errors":{"non_field_errors":[{"code":"server_username_groupname_invalid","message":"Invalid input."}]}}`))
					default:
						http.NotFound(w, r)
					}
				}))
				defer ts.Close()

				stdout, stderr, exit := runExecHelper(t, ts.URL, "--output", format, tt.target, "--", "uptime")

				assert.Equal(t, utils.ExitCodeGeneralError, exit, stderr)
				assert.Equal(t, int32(1), submitted.Load())
				assert.Empty(t, stdout)
				if format == utils.OutputFormatTable {
					assert.Equal(t, tt.wantTable, stderr)
					return
				}
				var envelope utils.JSONErrorEnvelope[map[string]any]
				require.NoError(t, json.Unmarshal([]byte(stderr), &envelope), stderr)
				assert.Equal(t, utils.ServerUsernameGroupnameInvalid, envelope.ErrorCode)
				assert.Equal(t, tt.wantJSON, envelope.Message)
				assert.Equal(t, tt.wantActions, envelope.NextActions)
				assert.Equal(t, http.StatusBadRequest, envelope.HTTPStatus)
				assert.Equal(t, map[string]any{"non_field_errors": []any{map[string]any{"code": "server_username_groupname_invalid", "message": "Invalid input."}}}, envelope.Server["field_errors"])
			})
		}
	}
}
