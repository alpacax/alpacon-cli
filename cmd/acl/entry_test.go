package acl

import (
	"testing"

	"github.com/alpacax/alpacon-cli/api/acl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseEntryFile(t *testing.T) {
	t.Parallel()
	data := []byte(`[
		{"principal_id": "3f6c1b2a-4d5e-4f60-8a7b-9c0d1e2f3a4b", "command_pattern": "uptime", "username": "*"},
		{"principal_id": "3f6c1b2a-4d5e-4f60-8a7b-9c0d1e2f3a4b", "command_pattern": "df -h", "username": "root",
		 "server_ids": ["6ba7b810-9dad-11d1-80b4-00c04fd430c8"], "expires_at": "2027-01-15T10:00:00Z", "enabled": false}
	]`)

	entries, err := parseEntryFile(data)

	require.NoError(t, err)
	assert.Equal(t, []acl.EntryRequest{
		{PrincipalType: "service_token", PrincipalID: "3f6c1b2a-4d5e-4f60-8a7b-9c0d1e2f3a4b", CommandPattern: "uptime", Username: "*", Enabled: true},
		{PrincipalType: "service_token", PrincipalID: "3f6c1b2a-4d5e-4f60-8a7b-9c0d1e2f3a4b", CommandPattern: "df -h", Username: "root",
			ServerIDs: []string{"6ba7b810-9dad-11d1-80b4-00c04fd430c8"}, ExpiresAt: "2027-01-15T10:00:00Z", Enabled: false},
	}, entries)
}

func TestParseEntryFile_Refusals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		data    string
		wantErr string
	}{
		{name: "not an array", data: `{"command_pattern": "uptime"}`, wantErr: "JSON array"},
		{name: "empty array", data: `[]`, wantErr: "no entries"},
		{name: "misspelled field", data: `[{"command_pattern": "uptime", "username": "*", "server_id": ["x"]}]`, wantErr: "server_id"},
		{name: "personal API token", data: `[{"principal_type": "api_token", "command_pattern": "uptime", "username": "*"}]`, wantErr: "no command list on the ledger"},
		{name: "unreadable expiry", data: `[{"command_pattern": "uptime", "username": "*", "expires_at": "tomorrow"}]`, wantErr: "entry 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseEntryFile([]byte(tt.data))
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestPartitionServerRefs(t *testing.T) {
	t.Parallel()
	ids, names := partitionServerRefs([]string{"web-01", " 6ba7b810-9dad-11d1-80b4-00c04fd430c8 ", "", "web-02"})

	assert.Equal(t, []string{"6ba7b810-9dad-11d1-80b4-00c04fd430c8"}, ids)
	assert.Equal(t, []string{"web-01", "web-02"}, names)
}

func TestValidateExpiry(t *testing.T) {
	t.Parallel()
	require.NoError(t, validateExpiry(""))
	require.NoError(t, validateExpiry("2027-01-15T10:00:00+09:00"))
	assert.ErrorContains(t, validateExpiry("2027-01-15"), "RFC 3339")
}
