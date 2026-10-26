package acl

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/api/acl"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fileTokenID = "3f6c1b2a-4d5e-4f60-8a7b-9c0d1e2f3a4b"

// fileItem is a valid --file item with fields overridden; a nil value drops the
// field.
func fileItem(overrides map[string]any) map[string]any {
	item := map[string]any{"principal_id": fileTokenID, "command_pattern": "uptime", "username": "*", "server_ids": []string{}}
	for key, value := range overrides {
		if value == nil {
			delete(item, key)
			continue
		}
		item[key] = value
	}
	return item
}

func fileOf(t *testing.T, items ...map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(items)
	require.NoError(t, err)
	return data
}

func TestParseEntryFile(t *testing.T) {
	t.Parallel()
	data := fileOf(t,
		fileItem(map[string]any{"command_pattern": "  uptime ", "username": " * "}),
		fileItem(map[string]any{
			"command_pattern": "df -h", "username": "root", "server_ids": []string{"6ba7b810-9dad-11d1-80b4-00c04fd430c8"},
			"expires_at": "2027-01-15T10:00:00Z", "enabled": false,
		}),
	)

	entries, err := parseEntryFile(data)

	require.NoError(t, err)
	assert.Equal(t, []acl.EntryRequest{
		{PrincipalType: "service_token", PrincipalID: fileTokenID, CommandPattern: "uptime", Username: "*", ServerIDs: []string{}, Enabled: true},
		{PrincipalType: "service_token", PrincipalID: fileTokenID, CommandPattern: "df -h", Username: "root",
			ServerIDs: []string{"6ba7b810-9dad-11d1-80b4-00c04fd430c8"}, ExpiresAt: "2027-01-15T10:00:00Z", Enabled: false},
	}, entries)
}

func TestParseEntryFile_AllowsTrailingWhitespace(t *testing.T) {
	t.Parallel()
	_, err := parseEntryFile(append(fileOf(t, fileItem(nil)), "\n\n"...))
	assert.NoError(t, err)
}

func TestParseEntryFile_Refusals(t *testing.T) {
	t.Parallel()
	valid := string(fileOf(t, fileItem(nil)))
	tests := []struct {
		name    string
		data    string
		wantErr string
	}{
		{name: "not an array", data: `{"command_pattern": "uptime"}`, wantErr: "JSON array"},
		{name: "empty array", data: `[]`, wantErr: "no entries"},
		{name: "misspelled field", data: string(fileOf(t, fileItem(map[string]any{"server_id": []string{"x"}}))), wantErr: "server_id"},
		{name: "trailing data", data: valid + ` {"unexpected": true}`, wantErr: "continues after it"},
		{name: "second array", data: `[] ` + valid, wantErr: "continues after it"},
		{name: "personal API token", data: string(fileOf(t, fileItem(map[string]any{"principal_type": "api_token"}))), wantErr: "entry 0: principal_type"},
		{name: "other principal type", data: string(fileOf(t, fileItem(map[string]any{"principal_type": "user"}))), wantErr: "entry 0: principal_type"},
		{name: "principal id missing", data: string(fileOf(t, fileItem(map[string]any{"principal_id": nil}))), wantErr: "entry 0: principal_id"},
		{name: "principal id not an id", data: string(fileOf(t, fileItem(map[string]any{"principal_id": "deploy-bot"}))), wantErr: "entry 0: principal_id"},
		{name: "blank command", data: string(fileOf(t, fileItem(map[string]any{"command_pattern": "  "}))), wantErr: "entry 0: command_pattern"},
		{name: "blank username", data: string(fileOf(t, fileItem(map[string]any{"username": " "}))), wantErr: "entry 0: username"},
		{name: "server ids omitted", data: string(fileOf(t, fileItem(map[string]any{"server_ids": nil}))), wantErr: "entry 0: server_ids is required"},
		{name: "server ids null", data: strings.Replace(valid, `"server_ids":[]`, `"server_ids":null`, 1), wantErr: "entry 0: server_ids is required"},
		{name: "empty server id", data: string(fileOf(t, fileItem(map[string]any{"server_ids": []string{""}}))), wantErr: "entry 0: a server value is empty"},
		{name: "second entry refused", data: string(fileOf(t, fileItem(nil), fileItem(map[string]any{"username": ""}))), wantErr: "entry 1: username"},
		{name: "unreadable expiry", data: string(fileOf(t, fileItem(map[string]any{"expires_at": "tomorrow"}))), wantErr: "entry 0: --expires-at must be an RFC 3339 time"},
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
	ids, names, err := partitionServerRefs([]string{"web-01", " 6ba7b810-9dad-11d1-80b4-00c04fd430c8 ", "web-02"})

	require.NoError(t, err)
	assert.Equal(t, []string{"6ba7b810-9dad-11d1-80b4-00c04fd430c8"}, ids)
	assert.Equal(t, []string{"web-01", "web-02"}, names)
}

// An empty scope means every server, so a stray empty value must not be read
// as "leave --server out".
func TestValidateServerRefs_RefusesAnEmptyValue(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		given bool
		refs  []string
	}{
		{name: "explicit empty flag", given: true, refs: nil},
		{name: "trailing comma", given: true, refs: []string{"web-01", ""}},
		{name: "blank component", given: true, refs: []string{"  "}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.ErrorIs(t, validateServerRefs(tt.given, tt.refs), errEmptyServerScope)
		})
	}
	require.NoError(t, validateServerRefs(false, nil), "leaving --server out covers every server on purpose")
	require.NoError(t, validateServerRefs(true, []string{"web-01"}))
}

// What cobra hands over for the shapes a person types: an explicit empty value
// arrives as no values with the flag marked changed, a stray comma as an empty
// component. Both have to reach the refusal.
func TestEntryAdd_ServerFlagEmptyShapesAreRefused(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"--server", ""}, {"--server", "web-01,"}, {"--server=,"}} {
		// A fresh command with the same flag type, so no parse leaks into
		// entryAddCmd.
		cmd := &cobra.Command{}
		cmd.Flags().StringSlice("server", nil, "")

		require.NoError(t, cmd.Flags().Parse(args))
		refs, err := cmd.Flags().GetStringSlice("server")
		require.NoError(t, err)
		assert.Error(t, validateServerRefs(cmd.Flags().Changed("server"), refs), "args %q", args)
	}
}

func TestValidateExpiry(t *testing.T) {
	t.Parallel()
	require.NoError(t, validateExpiry(""))
	require.NoError(t, validateExpiry("2027-01-15T10:00:00+09:00"))
	assert.ErrorContains(t, validateExpiry("2027-01-15"), "RFC 3339")
}
