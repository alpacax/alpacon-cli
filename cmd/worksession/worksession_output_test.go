package worksession

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api/types"
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/config"
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

	stdout, stderr := captureWorkSessionCommandOutput(t, func() {
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

	stdout, stderr := captureWorkSessionCommandOutput(t, func() {
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

	stdout, stderr := captureWorkSessionCommandOutput(t, func() {
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

	stdout, stderr := captureWorkSessionCommandOutput(t, func() {
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
	waitApproval = false
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
		waitApproval = false
		useAfterCreate = false
		createSudo = nil
		createSudoReason = ""
	})
}

func captureWorkSessionCommandOutput(t *testing.T, fn func()) (stdout string, stderr string) {
	t.Helper()
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	stdoutReader, stdoutWriter, err := os.Pipe()
	require.NoError(t, err)
	stderrReader, stderrWriter, err := os.Pipe()
	require.NoError(t, err)

	os.Stdout = stdoutWriter
	os.Stderr = stderrWriter
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()

	stdoutDone := make(chan string, 1)
	stderrDone := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, stdoutReader)
		stdoutDone <- buf.String()
	}()
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, stderrReader)
		stderrDone <- buf.String()
	}()

	fn()

	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()
	os.Stdout = oldStdout
	os.Stderr = oldStderr
	t.Cleanup(func() {
		_ = stdoutReader.Close()
		_ = stderrReader.Close()
	})

	return <-stdoutDone, <-stderrDone
}

func TestFormatDiffsAndRecommendation(t *testing.T) {
	assert.Equal(t, "command, logs → command",
		formatScopeDiff(&wsapi.ScopeDiff{Old: []string{"command", "logs"}, New: []string{"command"}}))
	assert.Equal(t, "web-01, db-01 → web-01",
		formatServerDiff(&wsapi.ServerDiff{
			Old: []types.ServerSummary{{Name: "web-01"}, {Name: "db-01"}},
			New: []types.ServerSummary{{Name: "web-01"}},
		}))
	assert.Equal(t, "[high] (admin_added) no destructive cmds",
		formatRecommendation(wsapi.WorkSessionRecommendation{Severity: "high", Source: "admin_added", Text: "no destructive cmds"}))
}

func TestWriteApprovalNotice(t *testing.T) {
	session := &wsapi.WorkSession{
		Adjustments: &wsapi.WorkSessionAdjustments{
			Scopes:  &wsapi.ScopeDiff{Old: []string{"command", "logs"}, New: []string{"command"}},
			Servers: &wsapi.ServerDiff{Old: []types.ServerSummary{{Name: "web-01"}, {Name: "db-01"}}, New: []types.ServerSummary{{Name: "web-01"}}},
		},
		Recommendations: []wsapi.WorkSessionRecommendation{
			{Severity: "high", Source: "admin_added", Text: "no destructive cmds"},
		},
	}
	var buf bytes.Buffer
	writeApprovalNotice(&buf, session)
	out := buf.String()
	assert.Contains(t, out, "scopes:  command, logs → command")
	assert.Contains(t, out, "servers: web-01, db-01 → web-01")
	assert.Contains(t, out, "[high] (admin_added) no destructive cmds")

	var empty bytes.Buffer
	writeApprovalNotice(&empty, &wsapi.WorkSession{})
	assert.Empty(t, empty.String())
}
