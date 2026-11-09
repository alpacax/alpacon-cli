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
	tests := []struct{ name, body, field, code string }{
		{"username", `{"detail":"Invalid input.","field_errors":{"username":[{"code":"service_token_account_not_own","message":"Invalid input."}]}}`, "username", "service_token_account_not_own"},
		{"groupname", `{"detail":"Invalid input.","field_errors":{"groupname":[{"code":"service_token_account_not_own","message":"Invalid input."}]}}`, "groupname", "service_token_account_not_own"},
		{"application account", `{"code":"application_system_user_not_found","detail":"Invalid input."}`, "", "application_system_user_not_found"},
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
			if tt.code == utils.ApplicationSystemUserNotFound {
				assert.Equal(t, "The service token's application cannot run as that account on that server. Run as an account bound to the application, or as its own account, and check that it is provisioned there (code: application_system_user_not_found).", err.Error())
			} else {
				assert.Contains(t, err.Error(), "token's application account")
			}
			assert.Contains(t, err.Error(), tt.code)
			assert.NotContains(t, err.Error(), "Invalid input.")
			if tt.field != "" {
				assert.Contains(t, err.Error(), tt.field+":")
				assert.True(t, utils.HasFieldErrorCode(err, tt.field, tt.code), "field code must survive rendering")
			}
			assert.Equal(t, http.StatusBadRequest, utils.HTTPStatusCode(err))
			assert.False(t, utils.IsUsageError(err), "a server refusal must remain exit 1")
		})
	}
}
