package worksession

import (
	"encoding/json"
	"testing"
	"time"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
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
