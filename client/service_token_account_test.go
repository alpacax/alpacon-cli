package client

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServiceTokenAccountRefusals(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, body, field, code, wantMessage string }{
		{"username", `{"detail":"Invalid input.","field_errors":{"username":[{"code":"service_token_account_not_own","message":"Invalid input."}]}}`, "username", "service_token_account_not_own", "username: the account must be the token's application account (code: service_token_account_not_own)"},
		{"groupname", `{"detail":"Invalid input.","field_errors":{"groupname":[{"code":"service_token_account_not_own","message":"Invalid input."}]}}`, "groupname", "service_token_account_not_own", "groupname: the group must be the token's application account's own group (code: service_token_account_not_own)"},
		{"application account", `{"code":"application_system_user_not_found","detail":"Invalid input."}`, "", "application_system_user_not_found", "the service token's application cannot run as that account on that server—run as an account bound to the application or as its own account, and check that it is provisioned there"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = fmt.Fprint(w, tt.body)
			}))
			defer ts.Close()
			ac := &AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
			_, err := ac.SendPostRequest("/api/test/", struct{}{})
			require.Error(t, err)
			assert.Equal(t, tt.wantMessage, err.Error())
			assert.NotContains(t, err.Error(), "Invalid input.")
			if tt.field != "" {
				assert.Contains(t, err.Error(), tt.field+":")
				assert.True(t, utils.HasFieldErrorCode(err, tt.field, tt.code), "field code must survive rendering")
			} else {
				assert.Equal(t, tt.code, utils.StructuredErrorCode(err))
			}
			assert.Equal(t, http.StatusBadRequest, utils.HTTPStatusCode(err))
			assert.False(t, utils.IsUsageError(err), "a server refusal must remain exit 1")
		})
	}
}
