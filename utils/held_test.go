package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// heldError carries a held request the way a command detail's error does.
type heldError struct {
	held *HeldRequest
}

func (e *heldError) Error() string           { return "sudo denied" }
func (e *heldError) ErrorHeld() *HeldRequest { return e.held }

// heldUnderAPIToken is a held error whose request also went out under an API token.
type heldUnderAPIToken struct{ heldError }

func (*heldUnderAPIToken) ErrorNextUnderAPIToken() bool { return true }

const (
	heldApprovalRequest = "3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47"
	heldStatusPath      = "/api/events/commands/550e8400-e29b-41d4-a716-446655440000/"
)

func heldAt(expiresAt *time.Time, statusPath string) *HeldRequest {
	return &HeldRequest{ApprovalRequest: heldApprovalRequest, ExpiresAt: expiresAt, StatusPath: statusPath}
}

func TestRefusalNextActions_Held(t *testing.T) {
	t.Parallel()
	future := time.Date(2999, 11, 6, 17, 20, 0, 0, time.FixedZone("KST", 9*60*60))
	past := time.Date(2001, 11, 6, 17, 20, 0, 0, time.FixedZone("KST", 9*60*60))
	reference := NextAction{Description: "Approval request: " + heldApprovalRequest}
	wait := NextAction{Command: "alpacon api " + heldStatusPath, Description: heldStatusPathDescription}
	tests := []struct {
		name string
		err  error
		want []NextAction
	}{
		{
			name: "reference then deadline then where to wait",
			err:  &heldError{held: heldAt(&future, heldStatusPath)},
			want: []NextAction{reference, {Description: "The approval must be decided by 2999-11-06T17:20:00+09:00"}, wait},
		},
		{
			name: "a past deadline asks for a new approval",
			err:  &heldError{held: heldAt(&past, heldStatusPath)},
			want: []NextAction{reference, {Description: "The approval deadline (2001-11-06T17:20:00+09:00) has passed; an approval given now cannot be used, so re-run the command to request a new one"}, wait},
		},
		{
			name: "no known deadline prints none",
			err:  &heldError{held: heldAt(nil, heldStatusPath)},
			want: []NextAction{reference, wait},
		},
		{
			name: "found through wrapping",
			err:  fmt.Errorf("failed to execute command: %w", &heldError{held: heldAt(nil, heldStatusPath)}),
			want: []NextAction{reference, wait},
		},
		{
			name: "an API token keeps the status path a command",
			err:  &heldUnderAPIToken{heldError{held: heldAt(nil, heldStatusPath)}},
			want: []NextAction{reference, wait},
		},
		{name: "a dot-dot segment drops the path", err: &heldError{held: heldAt(nil, "/api/events/../servers/")}, want: []NextAction{reference}},
		{name: "a dot segment drops the path", err: &heldError{held: heldAt(nil, "/api/./events/")}, want: []NextAction{reference}},
		{name: "an absolute URL drops the path", err: &heldError{held: heldAt(nil, "https://evil.example.com/api/events/commands/1/")}, want: []NextAction{reference}},
		{name: "a protocol-relative URL drops the path", err: &heldError{held: heldAt(nil, "//evil.example.com/api/")}, want: []NextAction{reference}},
		{name: "a query drops the path", err: &heldError{held: heldAt(nil, heldStatusPath+"?x=1")}, want: []NextAction{reference}},
		{name: "a null held adds nothing", err: &heldError{}},
		{name: "a plain error adds nothing", err: errors.New("boom")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, RefusalNextActions(tt.err))
		})
	}
}

func TestRefusalNextActions_HeldReferenceIsSanitized(t *testing.T) {
	t.Parallel()
	err := &heldError{held: &HeldRequest{ApprovalRequest: "req-1\x1b[2K\n"}}

	assert.Equal(t, []NextAction{{Description: "Approval request: req-1"}}, RefusalNextActions(err))
}

func TestHeldRequest_DecodesNullExpiry(t *testing.T) {
	t.Parallel()
	var held HeldRequest

	require.NoError(t, json.Unmarshal([]byte(`{"approval_request":"`+heldApprovalRequest+`","expires_at":null,"status_path":"`+heldStatusPath+`"}`), &held))

	assert.Nil(t, held.ExpiresAt)
	assert.False(t, held.Expired())
}

func TestHeldRequest_ParsesTheServerOffset(t *testing.T) {
	t.Parallel()
	var held HeldRequest

	require.NoError(t, json.Unmarshal([]byte(`{"expires_at":"2001-11-06T17:20:00+09:00"}`), &held))

	require.NotNil(t, held.ExpiresAt)
	assert.True(t, held.ExpiresAt.Equal(time.Date(2001, 11, 6, 8, 20, 0, 0, time.UTC)))
	assert.True(t, held.Expired())
}
