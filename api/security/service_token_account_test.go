package security

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
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
