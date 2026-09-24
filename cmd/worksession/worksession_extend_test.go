package worksession

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/client"
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
			name: "already pending",
			code: utils.WorkSessionExtensionAlreadyPending,
			want: "already has an extension request pending approval",
		},
		{
			name: "admission denied",
			code: utils.WorkSessionAdmissionDenied,
			want: "workspace policy denies this extension",
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

func TestPollForExtensionApproval_Approved(t *testing.T) {
	t.Parallel()
	newExpiry := time.Now().UTC().Add(4 * time.Hour).Truncate(time.Second)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/approvals/approvals/apr-1/":
			_, _ = w.Write([]byte(`{"id":"apr-1","status":"approved"}`))
		case "/api/work-sessions/sessions/ses-1/":
			_, _ = w.Write([]byte(`{"id":"ses-1","status":"active","expires_at":"` + newExpiry.Format(time.RFC3339) + `"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	session, err := pollForExtensionApproval(ac, "ses-1", "apr-1", time.Millisecond, time.Second)

	require.NoError(t, err)
	require.NotNil(t, session)
	assert.Nil(t, session.PendingExtensionRequest)
	assert.True(t, newExpiry.Equal(session.ExpiresAt))
}

func TestPollForExtensionApproval_TerminalStatusesAreDistinguishable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status  string
		wantMsg string
	}{
		{status: "rejected", wantMsg: "extension request was rejected"},
		{status: "expired", wantMsg: "extension request expired before it was decided"},
		{status: "cancelled", wantMsg: "extension request was cancelled"},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			t.Parallel()
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"apr-1","status":"` + tt.status + `"}`))
			}))
			defer ts.Close()
			ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

			_, err := pollForExtensionApproval(ac, "ses-1", "apr-1", time.Millisecond, time.Second)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantMsg)
			var terminal *terminalWaitError
			require.ErrorAs(t, err, &terminal, "a settled status must be typed so the caller can exit 6")
			assert.Equal(t, "ses-1", terminal.sessionID)
		})
	}
}

func TestPollForExtensionApproval_TimeoutIsNotTerminal(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"apr-1","status":"pending"}`))
	}))
	defer ts.Close()
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, err := pollForExtensionApproval(ac, "ses-1", "apr-1", time.Millisecond, 50*time.Millisecond)

	require.Error(t, err)
	var terminal *terminalWaitError
	assert.NotErrorAs(t, err, &terminal)
	var pending *pendingWaitError
	assert.ErrorAs(t, err, &pending)
}

func TestPollForExtensionApproval_APIFailureIsNotTerminal(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	_, err := pollForExtensionApproval(ac, "ses-1", "apr-1", time.Millisecond, time.Second)

	require.Error(t, err)
	var terminal *terminalWaitError
	assert.NotErrorAs(t, err, &terminal)
}

// TestExtendCommand_SendsReason covers the immediate (non-approval-gated) 200
// path end-to-end through the command's Run func: --reason flows onto the
// request body, and the printed result reads the server's own expires_at.
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

	_, stderr := testutil.CaptureOutput(t, func() {
		workSessionExtendCmd.Run(workSessionExtendCmd, []string{"ses-1"})
	})
	assert.Empty(t, stderr)
}

// TestExtendCommand_PendingApproval_NoWait covers the 202 shape end-to-end: the
// server names an extension request instead of applying it, and without --wait
// the CLI reports it pending and exits ExitCodePendingApproval (4) rather than
// treating it as a success or a failure. Runs as a subprocess (see
// worksession_error_test.go's runWorkSessionHelper) because this path calls
// os.Exit.
func TestExtendCommand_PendingApproval_NoWait(t *testing.T) {
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
		OK        bool   `json:"ok"`
		Status    string `json:"status"`
		ExitCode  int    `json:"exit_code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &env), "stdout: %s", stdout)
	assert.False(t, env.OK)
	assert.Equal(t, utils.PendingApprovalStatus, env.Status)
	assert.Equal(t, utils.ExitCodePendingApproval, env.ExitCode)
	assert.Equal(t, "apr-99", env.RequestID)
	assert.Contains(t, env.Message, "ses-1")
	assert.Contains(t, env.Message, "pending approval")
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
