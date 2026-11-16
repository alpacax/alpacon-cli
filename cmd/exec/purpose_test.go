package exec

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	execPurposeHelperMarker = "--exec-purpose-helper--"
	purposeJobID            = "a1b2c3d4-5678-abcd-ef01-234567890abc"
)

func runExecPurposeHelper(t *testing.T, workspaceURL string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	return runInProcess(t, workspaceURL, "purpose", "", args)
}

func runExecPurposeOK(t *testing.T, workspaceURL string, args ...string) string {
	t.Helper()
	_, stderr, exitCode := runExecPurposeHelper(t, workspaceURL, args...)
	require.Equal(t, 0, exitCode, "stderr: %s", stderr)
	return stderr
}

func longPurpose() string {
	b := make([]rune, PurposeMaxLength+1)
	for i := range b {
		b[i] = 'x'
	}
	return string(b)
}

func TestExecPurposeRejectsBadArgsWithUsageExit(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer ts.Close()

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"not a uuid", []string{"not-a-uuid", "clock skew"}, "invalid JOB_ID"},
		{"blank purpose", []string{purposeJobID, "   "}, "PURPOSE requires a value"},
		{"over the ceiling", []string{purposeJobID, longPurpose()}, "limited to 2000 characters"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, exitCode := runExecPurposeHelper(t, ts.URL, tc.args...)
			assert.Equal(t, utils.ExitCodeUsageError, exitCode)
			assert.Contains(t, stderr, tc.want)
		})
	}
}

// TestExecPurposeAcceptsAMultibytePurposeAtTheCeiling is the byte-vs-rune
// regression. The server counts characters, not bytes, against its 2000-character
// limit; a byte count refuses a Korean purpose at roughly 666 of them, while
// claiming the server would refuse it.
func TestExecPurposeAcceptsAMultibytePurposeAtTheCeiling(t *testing.T) {
	korean := make([]rune, PurposeMaxLength)
	for i := range korean {
		korean[i] = '한'
	}

	var body atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/"+purposeJobID+"/purpose/" {
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			body.Store(payload)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	runExecPurposeOK(t, ts.URL, purposeJobID, string(korean))

	sent, ok := body.Load().(map[string]any)
	require.True(t, ok, "a 2000-character Korean purpose was refused locally")
	assert.Equal(t, string(korean), sent["purpose"])
}

// TestExecPurposeAnswersTheDemand covers the happy path: a 202 and a pointer at
// where to read the outcome, never at a resubmit.
func TestExecPurposeAnswersTheDemand(t *testing.T) {
	var body atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/"+purposeJobID+"/purpose/" {
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			body.Store(payload)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	stderr := runExecPurposeOK(t, ts.URL, purposeJobID, "chronyd drifted 40s.")

	sent, ok := body.Load().(map[string]any)
	require.True(t, ok, "no purpose answer captured")
	assert.Equal(t, "chronyd drifted 40s.", sent["purpose"])
	assert.Contains(t, stderr, "Purpose recorded")
	assert.Contains(t, stderr, "alpacon exec logs "+purposeJobID)
	assert.NotContains(t, stderr, "re-run")
}

// TestExecPurposeTrimsBeforeSending pins that the value checked is the value
// sent: `--purpose '  x  '` must not be stored, judged, and written to the
// approver's card with the padding intact.
func TestExecPurposeTrimsBeforeSending(t *testing.T) {
	var body atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/events/commands/"+purposeJobID+"/purpose/" {
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			body.Store(payload)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	runExecPurposeOK(t, ts.URL, purposeJobID, "  clock skew  ")

	sent, ok := body.Load().(map[string]any)
	require.True(t, ok, "no purpose answer captured")
	assert.Equal(t, "clock skew", sent["purpose"])
}

// TestExecLogsShowsTheStatedPurpose pins that exec logs prints EventDetails.Purpose, sanitized, on
// stderr.
func TestExecLogsShowsTheStatedPurpose(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/events/commands/"+purposeJobID+"/" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      purposeJobID,
				"status":  "success",
				"success": true,
				"result":  "ok",
				// Server-supplied text written by one principal, printed to
				// another's terminal, so the escape must not survive.
				"purpose": "chronyd drifted 40s\x1b[31m",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	stdout, stderr, exitCode := runExecLogsHelper(t, ts.URL, utils.OutputFormatTable, purposeJobID)
	require.Equal(t, 0, exitCode, "stderr: %s", stderr)

	assert.Contains(t, stderr, "Stated purpose: chronyd drifted 40s")
	assert.NotContains(t, stderr, "\x1b[31m")
	// stdout stays the command's own output.
	assert.Contains(t, stdout, "ok")
	assert.NotContains(t, stdout, "Stated purpose")
}
