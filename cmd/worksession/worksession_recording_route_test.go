package worksession

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingRouteServer pages the timeline two items at a time and the records of
// wsh-2 as short pages that still carry a `next`, logging every request URL.
func recordingRouteServer(t *testing.T, recordsStatus int) (*httptest.Server, func() []string) {
	t.Helper()
	var (
		mu  sync.Mutex
		log []string
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		log = append(log, r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query()
		switch r.URL.Path {
		case "/api/work-sessions/sessions/ses-1/timeline/":
			if q.Get("cursor") == "" {
				_, _ = w.Write([]byte(`{"next":"p2","results":[{"type":"command","id":"c1"},{"type":"websh_session","id":"wsh-1"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"next":null,"results":[{"type":"tunnel_session","id":"tun-1","is_tunnel":true},{"type":"websh_session","id":"wsh-2"}]}`))
		case "/api/work-sessions/sessions/ses-1/records/":
			if recordsStatus != 0 {
				w.WriteHeader(recordsStatus)
				_, _ = w.Write([]byte(`{"code":"workspace_plan_update_required","gate":"plan"}`))
				return
			}
			switch {
			case q.Get("session") != "wsh-2":
				w.WriteHeader(http.StatusNotFound)
			case q.Get("cursor") == "":
				_, _ = w.Write([]byte(`{"next":"r2","results":[{"added_at":"2024-01-15T10:30:00Z","record":"one "}]}`))
			case q.Get("cursor") == "r2":
				_, _ = w.Write([]byte(`{"next":"r3","results":[{"added_at":"2024-01-15T10:30:01Z","record":"two "}]}`))
			default:
				_, _ = w.Write([]byte(`{"next":null,"results":[{"added_at":"2024-01-15T10:30:02Z","record":"three\n"}]}`))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), log...)
	}
}

func TestRecordingIndex_WalksTimelineOnceThenOnlyThatSessionsRecords(t *testing.T) {
	t.Parallel()
	ts, requests := recordingRouteServer(t, 0)

	stdout, stderr, exitCode := runWorkSessionHelper(t, utils.OutputFormatTable, ts.URL,
		"recording", "ses-1", "--index", "2")

	require.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assert.Contains(t, stdout, "Recording 2/2")
	assert.Contains(t, stdout, "one two three\n")

	var timeline, records []string
	for _, req := range requests() {
		assert.NotContains(t, req, "include_records=true")
		if strings.Contains(req, "/timeline/") {
			timeline = append(timeline, req)
		} else {
			records = append(records, req)
		}
	}
	// Two timeline pages (one walk), three records pages (one walk).
	require.Len(t, timeline, 2)
	assert.Contains(t, timeline[0], "cursor=")
	assert.Contains(t, timeline[1], "cursor=p2")
	require.Len(t, records, 3)
	for _, req := range records {
		assert.Contains(t, req, "session=wsh-2")
	}
	assert.Contains(t, records[0], "cursor=&")
	assert.Contains(t, records[1], "cursor=r2")
	assert.Contains(t, records[2], "cursor=r3")
}

func TestRecordingIndex_PlanLimitOnRecordsRoute(t *testing.T) {
	t.Parallel()
	ts, _ := recordingRouteServer(t, http.StatusPaymentRequired)

	stdout, stderr, exitCode := runWorkSessionHelper(t, utils.OutputFormatTable, ts.URL,
		"recording", "ses-1", "--index", "2")

	assert.Equal(t, 1, exitCode)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "This action needs a higher plan.")
	assert.NotContains(t, stderr, "workspace_plan_update_required")
}

func TestRecordingIndex_PastLastTerminalMakesNoRecordsRequest(t *testing.T) {
	t.Parallel()
	ts, requests := recordingRouteServer(t, 0)

	stdout, stderr, exitCode := runWorkSessionHelper(t, utils.OutputFormatTable, ts.URL,
		"recording", "ses-1", "--index", "3")

	assert.Equal(t, utils.ExitCodeUsageError, exitCode)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "out of range (session has 2 terminal(s))")
	for _, req := range requests() {
		assert.NotContains(t, req, "/records/")
	}
}
