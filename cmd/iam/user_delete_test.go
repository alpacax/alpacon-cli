package iam

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func TestUserDeleteArchiveHome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		flags     []string
		wantQuery string
	}{
		{name: "omitted"},
		{name: "explicit false", flags: []string{"--archive-home=false"}},
		{name: "accepted by a server that ignores the query", flags: []string{"--archive-home"}, wantQuery: "archive_home=true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			build := userDeleteTestCommand(t, tt.wantQuery, http.StatusNoContent, "")
			_, stderr, code := cmdtest.RunCaptured(build, append([]string{"alice", "-y"}, tt.flags...)...)
			assert.Equal(t, 0, code, stderr)
			assert.Contains(t, stderr, "User deleted: alice")
		})
	}
}

func TestUserDeleteCodedErrors(t *testing.T) {
	t.Parallel()

	for _, errorCode := range []string{"home_purge_removed", "system_home_conflict"} {
		t.Run(errorCode, func(t *testing.T) {
			t.Parallel()

			build := userDeleteTestCommand(t, "archive_home=true", http.StatusBadRequest, `{"code":"`+errorCode+`"}`)
			_, stderr, code := cmdtest.RunJSON(build, "alice", "-y", "--archive-home")
			assert.Equal(t, utils.ExitCodeGeneralError, code)
			assert.Contains(t, stderr, `"error_code":"`+errorCode+`"`)
		})
	}
}

func userDeleteTestCommand(t *testing.T, wantQuery string, status int, body string) cmdtest.Build {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/iam/users/-/{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"username":"operator","is_staff":true}`))
	})
	mux.HandleFunc("GET /api/iam/users/{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"550e8400-e29b-41d4-a716-446655440000","username":"alice"}]}`))
	})
	mux.HandleFunc("DELETE /api/iam/users/550e8400-e29b-41d4-a716-446655440000/{$}", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, wantQuery, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if body != "" {
			_, _ = w.Write([]byte(body))
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return func(ios *utils.IOStreams) *cobra.Command {
		return newUserDeleteCmd(cmdtest.ClientFactory(ts), ios)
	}
}
