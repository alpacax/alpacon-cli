package iam

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCanIQuiet_TheExitCodeIsTheAnswer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		allowed bool
	}{
		{"an allowed permission exits 0", true},
		{"a denied permission exits 1 and prints nothing", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/iam/users/-/":
					_ = json.NewEncoder(w).Encode(map[string]any{"id": "11111111-1111-1111-1111-111111111111", "username": "root"})
				case "/api/iam/users/-/permissions/":
					_ = json.NewEncoder(w).Encode(map[string]any{"allowed": tt.allowed})
				default:
					t.Errorf("unexpected request %s", r.URL)
					w.WriteHeader(http.StatusInternalServerError)
				}
			}))
			defer ts.Close()
			cmd := newUserPermissionCanICmd(func() (*client.AlpaconClient, error) {
				return &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}, nil
			})
			cmd.SetArgs([]string{"server:update", "-q"})

			err := cmd.Execute()

			if tt.allowed {
				assert.NoError(t, err)
				return
			}
			exitErr, ok := errors.AsType[*utils.ExitError](err)
			require.True(t, ok, "want an ExitError, got %v", err)
			assert.Equal(t, utils.ExitCodeGeneralError, exitErr.Code)
			var stderr bytes.Buffer
			utils.FprintExitError(&stderr, exitErr)
			assert.Empty(t, stderr.String())
		})
	}
}
