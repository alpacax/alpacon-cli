package event

import (
	"encoding/json"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventDetailsApprovedFileExecutionDecoding(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "absent", body: `{ "id": "cmd-1" }`},
		{name: "null", body: `{ "id": "cmd-1", "approved_file_execution": null }`},
		{name: "id only", body: `{ "id": "cmd-1", "approved_file_execution": {"id":"grant-1"} }`, want: `{"id":"grant-1"}`},
		{name: "full", body: `{ "id": "cmd-1", "approved_file_execution": {"id":"grant-1","expires_at":null,"approved_by":{"id":"user-1","username":"alice"},"approval_request":{"id":"request-1"},"revoked_at":null,"revoke_reason":null} }`, want: `{"id":"grant-1","expires_at":null,"approved_by":{"id":"user-1","username":"alice"},"approval_request":{"id":"request-1"},"revoked_at":null,"revoke_reason":null}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var details EventDetails
			require.NoError(t, json.Unmarshal([]byte(tt.body), &details))
			if tt.want == "" {
				assert.Nil(t, details.ApprovedFileExecution)
				return
			}
			require.NotNil(t, details.ApprovedFileExecution)
			assert.JSONEq(t, tt.want, string(*details.ApprovedFileExecution))
			list, err := json.Marshal(EventAttributes{ApprovedFileExecution: details.ApprovedFileExecution})
			require.NoError(t, err)
			var got map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(list, &got))
			assert.JSONEq(t, tt.want, string(got["approved_file_execution"]))
		})
	}
}

func TestStandingGrantLine(t *testing.T) {
	t.Parallel()
	expires := "2026-01-02T03:04:05Z"
	revoked := "2026-02-03T04:05:06Z"
	tests := []struct {
		name  string
		grant string
		want  string
	}{
		{name: "nil"},
		{name: "id only", grant: `{"id":"grant-1"}`, want: "Allowed by a standing grant (grant-1)"},
		{name: "full", grant: `{"id":"grant-1","expires_at":"2026-01-02T03:04:05Z","approved_by":{"id":"user-1","username":"alice"},"approval_request":{"id":"request-1"},"revoked_at":null,"revoke_reason":null}`, want: "Allowed by a standing grant (grant-1) approved by alice, expires " + utils.FormatTimestamp(expires)},
		{name: "full without expiry or approver", grant: `{"id":"grant-1","expires_at":null,"approved_by":null,"approval_request":null,"revoked_at":null,"revoke_reason":null}`, want: "Allowed by a standing grant (grant-1) approved by unknown, expires never recorded"},
		{name: "revoked", grant: `{"id":"grant-1","expires_at":null,"approved_by":{"id":"user-1","username":"alice"},"approval_request":null,"revoked_at":"2026-02-03T04:05:06Z","revoke_reason":"superseded"}`, want: "Allowed by a standing grant (grant-1) approved by alice, expires never recorded; revoked " + utils.FormatTimestamp(revoked) + ": superseded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var details EventDetails
			if tt.grant != "" {
				raw := json.RawMessage(tt.grant)
				details.ApprovedFileExecution = &raw
			}
			assert.Equal(t, tt.want, StandingGrantLine(details))
		})
	}
}
