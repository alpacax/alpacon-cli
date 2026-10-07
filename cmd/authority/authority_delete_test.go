package authority

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/api/cert"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deleteError returns the error cert.DeleteCA gives back through the real
// client when the server answers status with body.
func deleteError(t *testing.T, status int, contentType, body string) error {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	ac := &client.AlpaconClient{HTTPClient: server.Client(), BaseURL: server.URL, Token: "test-token"}
	err := cert.DeleteCA(ac, "test-ca-id")
	require.Error(t, err)
	return err
}

func TestDeleteCAErrorText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		contentType string
		body        string
		transport   error // set: the error never came from the server
		wantMatch   string
		wantNoMatch string
	}{
		{
			name:        "history refusal mapped",
			contentType: "application/json",
			body:        `{"code":"cert_authority_cannot_be_deleted"}`,
			wantMatch:   "disconnect the server it runs on and delete it",
			wantNoMatch: "Failed to delete the CA",
		},
		{
			name:        "another coded 400 falls back",
			contentType: "application/json",
			body:        `{"code":"cert_request_in_progress"}`,
			wantMatch:   "Failed to delete the CA",
			wantNoMatch: "disconnect the server",
		},
		{
			name:        "non-JSON 400 falls back",
			contentType: "text/html",
			body:        "<html>Bad Request</html>",
			wantMatch:   "Failed to delete the CA",
			wantNoMatch: "disconnect the server",
		},
		{
			name:      "transport error falls back",
			transport: errors.New("some network failure"),
			wantMatch: "Failed to delete the CA: some network failure",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.transport
			if err == nil {
				err = deleteError(t, http.StatusBadRequest, tt.contentType, tt.body)
			}
			got := deleteCAErrorText("my-authority", err)
			assert.Contains(t, got, tt.wantMatch)
			if tt.wantNoMatch != "" {
				assert.NotContains(t, got, tt.wantNoMatch)
			}
		})
	}
}
