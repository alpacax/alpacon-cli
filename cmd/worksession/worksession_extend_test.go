package worksession

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtendErrorMessage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		code string
		want string
	}{
		{
			name: "reason required",
			code: utils.WorkSessionExtensionReasonRequired,
			want: "--reason",
		},
		{
			name: "already pending names where to find the request id",
			code: utils.WorkSessionExtensionAlreadyPending,
			want: "pending_extension_request.id",
		},
		{
			name: "admission denied",
			code: utils.WorkSessionAdmissionDenied,
			want: "refuses this extension outright",
		},
		{
			name: "unknown code falls back to the generic message",
			code: "some_other_code",
			want: "Failed to extend work session",
		},
		{
			name: "no code falls back to the generic message",
			code: "",
			want: "Failed to extend work session",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body := `{"code":"` + tt.code + `"}`
			if tt.code == "" {
				body = `{"detail":"nope"}`
			}
			err := &testCodedError{code: tt.code, msg: body}
			got := extendErrorMessage("ses-1", err)
			assert.Contains(t, got, tt.want)
			// Every mapped message reads as its own sentence, capitalized like
			// the default fallback, not a lowercase continuation of "Error: ".
			assert.Regexp(t, `^[A-Z]`, got)
		})
	}
}

// testCodedError implements the codedError interface utils.ParseErrorResponse
// reads (ErrorCode/ErrorSource), without pulling in client.go's unexported
// apiError type.
type testCodedError struct {
	code string
	msg  string
}

func (e *testCodedError) Error() string       { return e.msg }
func (e *testCodedError) ErrorCode() string   { return e.code }
func (e *testCodedError) ErrorSource() string { return "" }

// TestExtendCommand_SendsReason covers the immediate (non-approval-gated) 200
// path end-to-end through the command's Run func: --reason flows onto the
// request body, and the printed JSON result carries the server's own
// expires_at (not just the value the flag sent).
func TestExtendCommand_SendsReason(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/work-sessions/sessions/ses-1/extend/" {
			http.NotFound(w, r)
			return
		}
		var got struct {
			ExpiresAt string `json:"expires_at"`
			Reason    string `json:"reason"`
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		assert.Equal(t, "customer escalation", got.Reason)
		_, _ = w.Write([]byte(`{"id":"ses-1","status":"active","expires_at":"2026-06-01T12:00:00Z"}`))
	}))
	defer ts.Close()
	setupWorkSessionCommandConfig(t, ts.URL)
	withWorkSessionCommandJSONMode(t)

	extendExpiresIn = ""
	extendExpiresAt = "2026-06-01T12:00:00Z"
	extendReason = "customer escalation"
	t.Cleanup(func() {
		extendExpiresIn = ""
		extendExpiresAt = ""
		extendReason = ""
	})

	stdout, stderr := testutil.CaptureOutput(t, func() {
		workSessionExtendCmd.Run(workSessionExtendCmd, []string{"ses-1"})
	})
	assert.Empty(t, stderr)

	var got struct {
		OK        bool   `json:"ok"`
		Operation string `json:"operation"`
		ExpiresAt string `json:"expires_at"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout: %s", stdout)
	assert.True(t, got.OK)
	assert.Equal(t, "extend", got.Operation)
	assert.Equal(t, "2026-06-01T12:00:00Z", got.ExpiresAt)
}

// TestExtendCommand_PendingApproval covers the 202 shape end-to-end: the
// server names an extension request instead of applying it, and the CLI
// reports it pending and exits ExitCodePendingApproval (4) rather than
// treating it as a success or a failure. The requested expiry and the
// request's own deadline land in the envelope's context, not only in the
// message text. Runs as a subprocess (see worksession_error_test.go's
// runWorkSessionHelper) because this path calls os.Exit.
func TestExtendCommand_PendingApproval(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.URL.Path != "/api/work-sessions/sessions/ses-1/extend/" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{
			"id": "ses-1",
			"status": "active",
			"expires_at": "2026-06-01T10:00:00Z",
			"pending_extension_request": {
				"id": "apr-99",
				"requested_expires_at": "2026-06-01T14:00:00Z",
				"expires_at": "2026-06-01T10:10:00Z",
				"reason": "customer escalation",
				"added_at": "2026-06-01T10:00:05Z"
			}
		}`))
	}))
	defer ts.Close()

	stdout, _, exitCode := runWorkSessionHelper(t, utils.OutputFormatJSON, ts.URL,
		"extend", "ses-1", "--expires-at", "2026-06-01T14:00:00Z", "--reason", "customer escalation")

	assert.Equal(t, utils.ExitCodePendingApproval, exitCode)

	var env struct {
		OK       bool   `json:"ok"`
		Status   string `json:"status"`
		ExitCode int    `json:"exit_code"`
		Message  string `json:"message"`
		Context  struct {
			Operation          string `json:"operation"`
			WorkSessionID      string `json:"work_session_id"`
			RequestID          string `json:"request_id"`
			RequestedExpiresAt string `json:"requested_expires_at"`
			RequestExpiresAt   string `json:"request_expires_at"`
		} `json:"context"`
		NextActions []struct {
			Command string `json:"command"`
		} `json:"next_actions"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &env), "stdout: %s", stdout)
	assert.False(t, env.OK)
	assert.Equal(t, utils.PendingApprovalStatus, env.Status)
	assert.Equal(t, utils.ExitCodePendingApproval, env.ExitCode)
	assert.Contains(t, env.Message, "ses-1")
	assert.Contains(t, env.Message, "pending approval")

	assert.Equal(t, "extend", env.Context.Operation)
	assert.Equal(t, "ses-1", env.Context.WorkSessionID)
	assert.Equal(t, "apr-99", env.Context.RequestID)
	assert.Equal(t, "2026-06-01T14:00:00Z", env.Context.RequestedExpiresAt)
	assert.Equal(t, "2026-06-01T10:10:00Z", env.Context.RequestExpiresAt)

	var sawDescribe bool
	for _, action := range env.NextActions {
		assert.NotContains(t, action.Command, "work-session complete", "complete has nothing to do with an extension decision")
		if action.Command == "alpacon work-session describe ses-1" {
			sawDescribe = true
		}
	}
	assert.True(t, sawDescribe, "next_actions must point at describe to check status")
}

// TestExtendCommand_StalePendingOn200_IsSuccess covers the case the status
// code (not the body field) exists to resolve: a 200 whose body still carries
// a pending_extension_request left over from before the workspace turned
// extension approval off, or from a request the periodic sweep has not yet
// cleared. The extension applied—status says so—so this must report success
// (exit 0), not pending.
func TestExtendCommand_StalePendingOn200_IsSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/work-sessions/sessions/ses-1/extend/" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{
			"id": "ses-1",
			"status": "active",
			"expires_at": "2026-06-01T12:00:00Z",
			"pending_extension_request": {
				"id": "apr-stale",
				"requested_expires_at": "2026-06-01T11:00:00Z",
				"expires_at": "2026-06-01T10:05:00Z",
				"reason": "an earlier, unrelated request",
				"added_at": "2026-06-01T09:00:00Z"
			}
		}`))
	}))
	defer ts.Close()
	setupWorkSessionCommandConfig(t, ts.URL)
	withWorkSessionCommandJSONMode(t)

	extendExpiresIn = ""
	extendExpiresAt = "2026-06-01T12:00:00Z"
	extendReason = ""
	t.Cleanup(func() {
		extendExpiresIn = ""
		extendExpiresAt = ""
	})

	stdout, stderr := testutil.CaptureOutput(t, func() {
		workSessionExtendCmd.Run(workSessionExtendCmd, []string{"ses-1"})
	})
	assert.Empty(t, stderr)

	var got struct {
		OK        bool   `json:"ok"`
		Operation string `json:"operation"`
		ExpiresAt string `json:"expires_at"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got), "stdout: %s", stdout)
	assert.True(t, got.OK)
	assert.Equal(t, "2026-06-01T12:00:00Z", got.ExpiresAt)
}

// TestExtendCommand_ReasonRequired_MapsToReadableMessage covers the CLI's own
// message for WORK_SESSION_EXTENSION_REASON_REQUIRED: the server sends no
// human detail on this code, so the friendly text has to come from
// extendErrorMessage rather than the generic fallback.
func TestExtendCommand_ReasonRequired_MapsToReadableMessage(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/api/work-sessions/sessions/ses-1/extend/" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":"work_session_extension_reason_required"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	stdout, stderr, exitCode := runWorkSessionHelper(t, utils.OutputFormatJSON, ts.URL,
		"extend", "ses-1", "--expires-in", "1h")

	assert.Equal(t, 1, exitCode)
	assert.Empty(t, stdout)

	var env errorEnvelope
	require.NoError(t, json.Unmarshal([]byte(stderr), &env), "stderr: %s", stderr)
	assert.False(t, env.OK)
	assert.Equal(t, "work_session_extension_reason_required", env.ErrorCode)
	assert.Contains(t, env.Message, "--reason")
}

// TestExtendCommand_AlreadyPending_MapsToReadableMessage covers the CLI's own
// message for WORK_SESSION_EXTENSION_ALREADY_PENDING, which likewise carries
// no human detail from the server.
func TestExtendCommand_AlreadyPending_MapsToReadableMessage(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/api/work-sessions/sessions/ses-1/extend/" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":"work_session_extension_already_pending"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	stdout, stderr, exitCode := runWorkSessionHelper(t, utils.OutputFormatJSON, ts.URL,
		"extend", "ses-1", "--expires-in", "1h", "--reason", "customer escalation")

	assert.Equal(t, 1, exitCode)
	assert.Empty(t, stdout)

	var env errorEnvelope
	require.NoError(t, json.Unmarshal([]byte(stderr), &env), "stderr: %s", stderr)
	assert.False(t, env.OK)
	assert.Equal(t, "work_session_extension_already_pending", env.ErrorCode)
	assert.Contains(t, env.Message, "pending_extension_request.id")
}
