package security

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAclAdd_OmitsUsername(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"command", "file"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); !assert.NoError(t, err) {
					w.WriteHeader(500)
					return
				}
				assert.NotContains(t, body, "username")
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{}`)
			}))
			defer ts.Close()
			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
			if kind == "command" {
				require.NoError(t, AddCommandAcl(ac, CommandAclRequest{Token: "token", Command: "uptime"}))
			} else {
				require.NoError(t, AddFileAcl(ac, FileAclRequest{Token: "token", Path: "/tmp/*", Action: "upload"}))
			}
		})
	}
}

func TestAclList_AppliesToToken(t *testing.T) {
	old := utils.OutputFormat
	t.Cleanup(func() { utils.OutputFormat = old })
	for _, kind := range []string{"command", "file"} {
		t.Run(kind, func(t *testing.T) {
			for _, tt := range []struct{ name, field, mark string }{{"true", `,"applies_to_token":true`, ""}, {"false", `,"applies_to_token":false`, "no"}, {"absent", "", ""}} {
				t.Run(tt.name, func(t *testing.T) {
					raw := `{"id":"entry","token_name":"deploy","command":"uptime","path":"/tmp/*","action":"upload","username":"deploy","groupname":""` + tt.field + `}`
					ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", "application/json")
						_, _ = fmt.Fprintf(w, `{"count":1,"next":null,"results":[%s]}`, raw)
					}))
					defer ts.Close()
					ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
					var rows any
					if kind == "command" {
						v, err := GetCommandAclList(ac, "token")
						require.NoError(t, err)
						rows = v
					} else {
						v, err := GetFileAclList(ac, "token")
						require.NoError(t, err)
						rows = v
					}
					utils.OutputFormat = utils.OutputFormatJSON
					out, _ := testutil.CaptureOutput(t, func() { utils.PrintTable(rows) })
					var got []map[string]any
					require.NoError(t, json.Unmarshal([]byte(out), &got))
					var want map[string]any
					require.NoError(t, json.Unmarshal([]byte(raw), &want))
					if kind == "command" {
						delete(want, "path")
						delete(want, "action")
					} else {
						delete(want, "command")
					}
					expected, err := json.Marshal([]map[string]any{want})
					require.NoError(t, err)
					assert.JSONEq(t, string(expected), out)
					utils.OutputFormat = utils.OutputFormatTable
					out, _ = testutil.CaptureOutput(t, func() { utils.PrintTable(rows) })
					assert.Contains(t, out, "APPLIES")
					if tt.mark != "" {
						assert.Contains(t, out, "no")
					} else {
						assert.NotContains(t, out, "no")
					}
				})
			}
		})
	}
}
