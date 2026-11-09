package event

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventDetails_DecodesHeld(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, body string
		wantHeld   bool
		wantExpiry bool
	}{
		{name: "a held object", body: `{"held":{"approval_request":"3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47","expires_at":"2026-11-06T17:20:00+09:00","status_path":"/api/events/commands/550e8400-e29b-41d4-a716-446655440000/"}}`, wantHeld: true, wantExpiry: true},
		{name: "a null expiry", body: `{"held":{"approval_request":"3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47","expires_at":null,"status_path":"/api/events/commands/550e8400-e29b-41d4-a716-446655440000/"}}`, wantHeld: true},
		{name: "held null", body: `{"held":null}`},
		{name: "an older server sends no key", body: `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var details EventDetails

			require.NoError(t, json.Unmarshal([]byte(tt.body), &details))

			if !tt.wantHeld {
				assert.Nil(t, details.Held)
				return
			}
			require.NotNil(t, details.Held)
			assert.Equal(t, "3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47", details.Held.ApprovalRequest)
			assert.Equal(t, "/api/events/commands/550e8400-e29b-41d4-a716-446655440000/", details.Held.StatusPath)
			assert.Equal(t, tt.wantExpiry, details.Held.ExpiresAt != nil)
		})
	}
}

func TestErrorFromDetails_SudoDenialCarriesHeld(t *testing.T) {
	t.Parallel()
	var details EventDetails
	require.NoError(t, json.Unmarshal([]byte(`{"status":"completed","success":false,"exit_code":1,"held":{"approval_request":"3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47","expires_at":null,"status_path":"/api/events/commands/cmd-9/"}}`), &details))

	err := errorFromDetails(testPathID(t, "cmd-9"), details)

	var remoteErr *RemoteCommandError
	require.ErrorAs(t, err, &remoteErr)
	assert.Same(t, details.Held, remoteErr.ErrorHeld())
}
