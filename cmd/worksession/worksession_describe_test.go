package worksession

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	var runErr error
	stdout, _ := testutil.CaptureOutput(t, func() {
		runErr = workSessionDescribeCmd.RunE(workSessionDescribeCmd, []string{"ses-x"})
	})
	require.NoError(t, runErr)

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

	var runErr error
	stdout, _ := testutil.CaptureOutput(t, func() {
		runErr = workSessionDescribeCmd.RunE(workSessionDescribeCmd, []string{"ses-k"})
	})
	require.NoError(t, runErr)

	assert.Regexp(t, `(?m)^Clusters\s+prod-east$`, stdout)
	assert.Regexp(t, `(?m)^Expires at\s*$`, stdout)
	assert.NotContains(t, stdout, "0001-01-01")
}

// A caller who does not manage the session is named only the targets it reaches, and the rows
// say how many more there are; JSON passes the counts through as sent.
func TestWorkSessionDescribeSaysHowManyTheCallerDoesNotReach(t *testing.T) {
	const body = `{
		"id":"550e8400-e29b-41d4-a716-446655440000","status":"active","added_at":"2026-05-30T00:00:00Z",
		"servers":[{"id":"3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47","name":"web-1"}],"servers_hidden":1,
		"clusters":[],"clusters_hidden":0,
		"adjustments":{"servers":{"old":[{"name":"web-1"}],"new":[],"old_hidden":2,"new_hidden":1}}
	}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/work-sessions/sessions/550e8400-e29b-41d4-a716-446655440000/" {
			_, _ = w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	setupWorkSessionCommandConfig(t, ts.URL)

	t.Run("table", func(t *testing.T) {
		var runErr error
		stdout, _ := testutil.CaptureOutput(t, func() {
			runErr = workSessionDescribeCmd.RunE(workSessionDescribeCmd, []string{"550e8400-e29b-41d4-a716-446655440000"})
		})
		require.NoError(t, runErr)
		assert.Regexp(t, `(?m)^Servers\s+web-1 and 1 more$`, stdout)
		assert.Regexp(t, `(?m)^Clusters\s*$`, stdout)
		assert.Contains(t, stdout, "servers: web-1 and 2 more → 1 server you cannot access")
	})

	t.Run("json", func(t *testing.T) {
		withWorkSessionCommandJSONMode(t)
		var runErr error
		stdout, _ := testutil.CaptureOutput(t, func() {
			runErr = workSessionDescribeCmd.RunE(workSessionDescribeCmd, []string{"550e8400-e29b-41d4-a716-446655440000"})
		})
		require.NoError(t, runErr)
		assert.JSONEq(t, body, stdout)
	})
}

func TestWorkSessionDescribeShowsAHiddenCluster(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/work-sessions/sessions/550e8400-e29b-41d4-a716-446655440000/" {
			_, _ = w.Write([]byte(`{
				"id":"550e8400-e29b-41d4-a716-446655440000","status":"active","scopes":["kube"],"added_at":"2026-05-30T00:00:00Z",
				"servers":[],"servers_hidden":0,"clusters":[],"clusters_hidden":1
			}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	setupWorkSessionCommandConfig(t, ts.URL)

	var runErr error
	stdout, _ := testutil.CaptureOutput(t, func() {
		runErr = workSessionDescribeCmd.RunE(workSessionDescribeCmd, []string{"550e8400-e29b-41d4-a716-446655440000"})
	})
	require.NoError(t, runErr)
	assert.Regexp(t, `(?m)^Clusters\s+1 cluster you cannot access$`, stdout)
	assert.Regexp(t, `(?m)^Servers\s*$`, stdout)
}
