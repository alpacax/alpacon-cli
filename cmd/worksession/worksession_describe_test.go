package worksession

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/testutil"
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

	stdout, _ := testutil.CaptureOutput(t, func() {
		workSessionDescribeCmd.Run(workSessionDescribeCmd, []string{"ses-x"})
	})

	assert.Contains(t, stdout, "Adjustments:")
	assert.Contains(t, stdout, "servers: web-01, db-01 → web-01")
	assert.Contains(t, stdout, "Recommendations:")
	assert.Contains(t, stdout, "[HIGH] Rotate the key")
}

// A cluster session shows its cluster, and one created with a duration shows
// no expiry until it activates rather than year one.
func TestWorkSessionDescribeShowsAClusterSession(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/work-sessions/sessions/ses-k/" {
			_, _ = w.Write([]byte(`{
				"id":"ses-k","status":"pending","scopes":["kube","kube_exec"],"servers":[],
				"clusters":[{"id":"c-1","name":"prod-east"}],"added_at":"2026-05-30T00:00:00Z"
			}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	setupWorkSessionCommandConfig(t, ts.URL)

	stdout, _ := testutil.CaptureOutput(t, func() {
		workSessionDescribeCmd.Run(workSessionDescribeCmd, []string{"ses-k"})
	})

	assert.Regexp(t, `(?m)^Clusters\s+prod-east$`, stdout)
	assert.Regexp(t, `(?m)^Expires at\s*$`, stdout)
	assert.NotContains(t, stdout, "0001-01-01")
}
