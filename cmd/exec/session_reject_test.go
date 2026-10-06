package exec

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

const sessionRejectDetail = "Event sessions are disabled for this workspace."

func newSessionRejectingServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/sessions/":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"detail":"` + sessionRejectDetail + `"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/":
			_, _ = w.Write([]byte(`[{"id":"cmd-1"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/cmd-1/":
			_, _ = w.Write([]byte(`{"id":"cmd-1","status":"completed","success":true,"result":"done\n"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/cmd-1/chunks/":
			_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestExecFallbackWarnsWithTheServersReasonWhenTheSessionIsRejected(t *testing.T) {
	t.Parallel()
	ts := newSessionRejectingServer()
	defer ts.Close()

	stdout, stderr, exitCode := runExecHelper(t, ts.URL, "prod", "--", "echo", "done")

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assert.Contains(t, stdout, "done")
	assert.Contains(t, stderr, sessionRejectDetail)
	assert.Contains(t, stderr, "falling back to polling")
	assert.NotContains(t, stderr, "connect timeout")
}
