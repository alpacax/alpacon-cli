package worksession

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api/types"
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkSessionMutationOutputWrapsSession(t *testing.T) {
	expiresAt := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	session := &wsapi.WorkSession{
		ID:                "ses-abc",
		Description:       "incident",
		Status:            "active",
		ApprovalRequestID: "apr-1",
		ExpiresAt:         expiresAt,
	}
	active := session.ID

	output := newWorkSessionMutationOutput("create", "created", session, &active)
	body, err := json.Marshal(output)
	require.NoError(t, err)

	var got struct {
		OK                bool   `json:"ok"`
		Operation         string `json:"operation"`
		Message           string `json:"message"`
		WorkSessionID     string `json:"work_session_id"`
		Status            string `json:"status"`
		ExpiresAt         string `json:"expires_at"`
		ApprovalRequestID string `json:"approval_request_id"`
		ActiveWorksession string `json:"active_worksession"`
		WorkSession       struct {
			ID          string `json:"id"`
			Description string `json:"description"`
			Status      string `json:"status"`
		} `json:"work_session"`
	}
	require.NoError(t, json.Unmarshal(body, &got))
	assert.True(t, got.OK)
	assert.Equal(t, "create", got.Operation)
	assert.Equal(t, "created", got.Message)
	assert.Equal(t, "ses-abc", got.WorkSessionID)
	assert.Equal(t, "active", got.Status)
	assert.Equal(t, "2026-06-01T12:00:00Z", got.ExpiresAt)
	assert.Equal(t, "apr-1", got.ApprovalRequestID)
	assert.Equal(t, "ses-abc", got.ActiveWorksession)
	assert.Equal(t, "ses-abc", got.WorkSession.ID)
	assert.Equal(t, "incident", got.WorkSession.Description)
	assert.Equal(t, "active", got.WorkSession.Status)
}

func TestWorkSessionExtendOutput(t *testing.T) {
	output := newWorkSessionExtendOutput("ses-abc", "2026-06-01T12:00:00Z")
	body, err := json.Marshal(output)
	require.NoError(t, err)

	assert.JSONEq(t, `{
		"ok": true,
		"operation": "extend",
		"message": "Work session ses-abc extended to 2026-06-01T12:00:00Z.",
		"work_session_id": "ses-abc",
		"expires_at": "2026-06-01T12:00:00Z",
		"active_worksession": null
	}`, string(body))
}

func TestWorkSessionCancelOutput(t *testing.T) {
	output := newWorkSessionCancelOutput("ses-abc")
	body, err := json.Marshal(output)
	require.NoError(t, err)

	assert.JSONEq(t, `{
		"ok": true,
		"operation": "cancel",
		"message": "Work session ses-abc cancelled.",
		"work_session_id": "ses-abc",
		"status": "cancelled",
		"active_worksession": null
	}`, string(body))
}

func TestWorkSessionCreateCommandJSONOutput_NoHumanSuccessText(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"prod"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/work-sessions/sessions/":
			_, _ = w.Write([]byte(`{
				"id": "ses-created",
				"description": "incident",
				"status": "active",
				"requester_type": "user",
				"scopes": ["command"],
				"servers": [{"id":"srv-1","name":"prod"}],
				"approval_request_id": "",
				"expires_at": "2026-06-01T12:00:00Z",
				"added_at": "2026-05-30T00:00:00Z",
				"updated_at": "2026-05-30T00:00:00Z"
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	setupWorkSessionCommandConfig(t, ts.URL)
	withWorkSessionCommandJSONMode(t)
	resetCreateCommandState(t)
	purpose = "incident"
	createScopes = []string{"command"}
	createServers = []string{"prod"}
	expiresAt = "2026-06-01T12:00:00Z"
	requesterType = "user"

	stdout, stderr := testutil.CaptureOutput(t, func() {
		workSessionCreateCmd.Run(workSessionCreateCmd, nil)
	})

	assert.Empty(t, stderr)
	assert.NotContains(t, stdout, "Success:")
	assert.NotContains(t, stderr, "Success:")

	var got struct {
		OK                bool    `json:"ok"`
		Operation         string  `json:"operation"`
		WorkSessionID     string  `json:"work_session_id"`
		Status            string  `json:"status"`
		ActiveWorksession *string `json:"active_worksession"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.True(t, got.OK)
	assert.Equal(t, "create", got.Operation)
	assert.Equal(t, "ses-created", got.WorkSessionID)
	assert.Equal(t, "active", got.Status)
	assert.Nil(t, got.ActiveWorksession)
}

func TestWorkSessionUseCommandJSONOutput_NoHumanSuccessText(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet || r.URL.Path != "/api/work-sessions/sessions/ses-active/" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{
			"id": "ses-active",
			"description": "incident",
			"status": "active",
			"expires_at": "2026-06-01T12:00:00Z"
		}`))
	}))
	defer ts.Close()

	setupWorkSessionCommandConfig(t, ts.URL)
	withWorkSessionCommandJSONMode(t)
	unsetActiveWorkSession = false
	t.Cleanup(func() { unsetActiveWorkSession = false })

	stdout, stderr := testutil.CaptureOutput(t, func() {
		workSessionUseCmd.Run(workSessionUseCmd, []string{"ses-active"})
	})

	assert.Empty(t, stderr)
	assert.NotContains(t, stdout, "Success:")
	assert.NotContains(t, stderr, "Success:")

	var got struct {
		OK                bool    `json:"ok"`
		Operation         string  `json:"operation"`
		WorkSessionID     string  `json:"work_session_id"`
		ActiveWorksession *string `json:"active_worksession"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.True(t, got.OK)
	assert.Equal(t, "use", got.Operation)
	assert.Equal(t, "ses-active", got.WorkSessionID)
	require.NotNil(t, got.ActiveWorksession)
	assert.Equal(t, "ses-active", *got.ActiveWorksession)

	active, err := config.GetActiveWorkSession()
	require.NoError(t, err)
	assert.Equal(t, "ses-active", active)
}

func TestWorkSessionUnsetCommandJSONOutput_OperationIsUnset(t *testing.T) {
	setupWorkSessionCommandConfig(t, "http://example.invalid")
	withWorkSessionCommandJSONMode(t)
	require.NoError(t, config.SetActiveWorkSession("ses-active"))
	unsetActiveWorkSession = true
	t.Cleanup(func() { unsetActiveWorkSession = false })

	stdout, stderr := testutil.CaptureOutput(t, func() {
		workSessionUseCmd.Run(workSessionUseCmd, nil)
	})

	assert.Empty(t, stderr)

	var got struct {
		OK                bool    `json:"ok"`
		Operation         string  `json:"operation"`
		ActiveWorksession *string `json:"active_worksession"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.True(t, got.OK)
	assert.Equal(t, "unset", got.Operation)
	assert.Nil(t, got.ActiveWorksession)
}

func TestWorkSessionExtendCommandJSONOutput_NoHumanSuccessText(t *testing.T) {
	var sawExtend bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.URL.Path != "/api/work-sessions/sessions/ses-active/extend/" {
			http.NotFound(w, r)
			return
		}
		sawExtend = true
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	setupWorkSessionCommandConfig(t, ts.URL)
	withWorkSessionCommandJSONMode(t)
	extendExpiresIn = ""
	extendExpiresAt = "2026-06-01T12:00:00Z"
	t.Cleanup(func() {
		extendExpiresIn = ""
		extendExpiresAt = ""
	})

	stdout, stderr := testutil.CaptureOutput(t, func() {
		workSessionExtendCmd.Run(workSessionExtendCmd, []string{"ses-active"})
	})

	assert.True(t, sawExtend)
	assert.Empty(t, stderr)
	assert.NotContains(t, stdout, "Success:")
	assert.NotContains(t, stderr, "Success:")

	var got struct {
		OK            bool   `json:"ok"`
		Operation     string `json:"operation"`
		WorkSessionID string `json:"work_session_id"`
		ExpiresAt     string `json:"expires_at"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.True(t, got.OK)
	assert.Equal(t, "extend", got.Operation)
	assert.Equal(t, "ses-active", got.WorkSessionID)
	assert.Equal(t, "2026-06-01T12:00:00Z", got.ExpiresAt)
}

func TestWorkSessionCancelCommandJSONOutput_NoHumanSuccessText(t *testing.T) {
	var sawCancel bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.URL.Path != "/api/work-sessions/sessions/ses-pending/cancel/" {
			http.NotFound(w, r)
			return
		}
		sawCancel = true
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	setupWorkSessionCommandConfig(t, ts.URL)
	withWorkSessionCommandJSONMode(t)

	stdout, stderr := testutil.CaptureOutput(t, func() {
		workSessionCancelCmd.Run(workSessionCancelCmd, []string{"ses-pending"})
	})

	assert.True(t, sawCancel)
	assert.Empty(t, stderr)
	assert.NotContains(t, stdout, "Success:")

	var got struct {
		OK            bool   `json:"ok"`
		Operation     string `json:"operation"`
		WorkSessionID string `json:"work_session_id"`
		Status        string `json:"status"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.True(t, got.OK)
	assert.Equal(t, "cancel", got.Operation)
	assert.Equal(t, "ses-pending", got.WorkSessionID)
	assert.Equal(t, "cancelled", got.Status)
}

func TestFormatAdjustments(t *testing.T) {
	tests := []struct {
		name string
		adj  *wsapi.Adjustments
		want string
	}{
		{"nil", nil, ""},
		{
			"scopes only",
			&wsapi.Adjustments{Scopes: &wsapi.ScopeDiff{Old: []string{"command", "websh"}, New: []string{"command"}}},
			"  scopes:  command, websh → command",
		},
		{
			"servers only",
			&wsapi.Adjustments{Servers: &wsapi.ServerDiff{
				Old: []types.ServerSummary{{Name: "web-01"}, {Name: "db-01"}},
				New: []types.ServerSummary{{Name: "web-01"}},
			}},
			"  servers: web-01, db-01 → web-01",
		},
		{
			"both",
			&wsapi.Adjustments{
				Scopes:  &wsapi.ScopeDiff{Old: []string{"command"}, New: []string{}},
				Servers: &wsapi.ServerDiff{Old: []types.ServerSummary{{Name: "web-01"}}, New: []types.ServerSummary{{Name: "web-01"}}},
			},
			"  scopes:  command → none\n  servers: web-01 → web-01",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, formatAdjustments(tt.adj))
		})
	}
}

func TestFormatRecommendations(t *testing.T) {
	assert.Equal(t, "", formatRecommendations(nil))
	assert.Equal(t, "", formatRecommendations([]wsapi.Recommendation{}))

	got := formatRecommendations([]wsapi.Recommendation{
		{Severity: "high", Text: "Rotate the key"},
		{Severity: "low", Text: "Prefer reload"},
		{Severity: "", Text: "No severity set"},
		{Severity: "\x1b[2K\r", Text: "Severity left empty by stripping"},
	})
	assert.Equal(t, "  [HIGH] Rotate the key\n  [LOW] Prefer reload\n  [INFO] No severity set\n  [INFO] Severity left empty by stripping", got)
}

func TestFormatAdvisories_StripsControlSequences(t *testing.T) {
	// This block is where a requester reads what was granted, so a control
	// sequence in any interpolated field can hide it.
	const payload = "reboot db-01\x1b[2K\rapproved: read-only access"
	tests := []struct {
		name string
		got  func() string
		// Severity is uppercased before interpolation, so that case spells the
		// payload its own way. The server whitelists severity today; the case
		// stays because this sanitisation does not rely on that.
		wantParts []string
	}{
		{
			"recommendation text",
			func() string {
				return formatRecommendations([]wsapi.Recommendation{{Severity: "high", Text: payload}})
			},
			[]string{"reboot db-01", "approved: read-only access"},
		},
		{
			"recommendation severity",
			func() string {
				return formatRecommendations([]wsapi.Recommendation{{Severity: payload, Text: "rotate the key"}})
			},
			[]string{"REBOOT DB-01", "APPROVED: READ-ONLY ACCESS"},
		},
		{
			"server name",
			func() string {
				return formatAdjustments(&wsapi.Adjustments{Servers: &wsapi.ServerDiff{
					Old: []types.ServerSummary{{Name: payload}},
					New: []types.ServerSummary{{Name: "web-01"}},
				}})
			},
			[]string{"reboot db-01", "approved: read-only access"},
		},
		{
			"scope name",
			func() string {
				return formatAdjustments(&wsapi.Adjustments{Scopes: &wsapi.ScopeDiff{
					Old: []string{payload},
					New: []string{"command"},
				}})
			},
			[]string{"reboot db-01", "approved: read-only access"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.got()
			assert.NotContains(t, got, "\x1b")
			assert.NotContains(t, got, "\r")
			for _, part := range tt.wantParts {
				assert.Contains(t, got, part)
			}
		})
	}
}

func TestFormatAdvisories_KeepsOneLinePerEntry(t *testing.T) {
	// The formatters join entries with \n, so a newline inside a value forges an
	// extra advisory line.
	got := formatRecommendations([]wsapi.Recommendation{{Severity: "high", Text: "granted\n  [HIGH] full root access"}})
	assert.Len(t, strings.Split(got, "\n"), 1)
}

func setupWorkSessionCommandConfig(t *testing.T, workspaceURL string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	require.NoError(t, config.CreateConfig(workspaceURL, "ws", "token", "", "", "", "", 0, false))
}

func withWorkSessionCommandJSONMode(t *testing.T) {
	t.Helper()
	old := utils.OutputFormat
	utils.OutputFormat = utils.OutputFormatJSON
	t.Cleanup(func() { utils.OutputFormat = old })
}

func resetCreateCommandState(t *testing.T) {
	t.Helper()
	purpose = ""
	createScopes = nil
	createServers = nil
	expiresIn = ""
	expiresAt = ""
	requesterType = "user"
	wait = false
	waitApproval = ""
	useAfterCreate = false
	createSudo = nil
	createSudoReason = ""
	t.Cleanup(func() {
		purpose = ""
		createScopes = nil
		createServers = nil
		expiresIn = ""
		expiresAt = ""
		requesterType = "user"
		wait = false
		waitApproval = ""
		useAfterCreate = false
		createSudo = nil
		createSudoReason = ""
	})
}
