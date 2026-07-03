package worksession

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWorkSessionDescribePrintsAdvisories(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/work-sessions/sessions/ses-x/" {
			_, _ = w.Write([]byte(`{
				"id":"ses-x","status":"approved","expires_at":"2026-06-01T12:00:00Z","added_at":"2026-05-30T00:00:00Z",
				"adjustments":{"servers":{"old":[{"name":"web-01"},{"name":"db-01"}],"new":[{"name":"web-01"}]}},
				"recommendations":[{"id":"r1","text":"Rotate the key","severity":"high"}]
			}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	setupWorkSessionCommandConfig(t, ts.URL)

	stdout, _ := captureWorkSessionCommandOutput(t, func() {
		workSessionDescribeCmd.Run(workSessionDescribeCmd, []string{"ses-x"})
	})

	assert.Contains(t, stdout, "Adjustments:")
	assert.Contains(t, stdout, "servers: web-01, db-01 → web-01")
	assert.Contains(t, stdout, "Recommendations:")
	assert.Contains(t, stdout, "[HIGH] Rotate the key")
}
