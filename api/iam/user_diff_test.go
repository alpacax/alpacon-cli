package iam

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiffEditedUser(t *testing.T) {
	original := []byte(`{
	  "id": "u-1",
	  "username": "john",
	  "phone": "",
	  "is_active": true,
	  "is_staff": false,
	  "is_superuser": true,
	  "is_ldap_user": true,
	  "num_groups": 2
	}`)

	tests := []struct {
		name           string
		edited         map[string]any
		wantChanges    map[string]any
		wantPrivileges []PrivilegeEdit
	}{
		{
			name:        "an untouched buffer changes nothing",
			edited:      map[string]any{"id": "u-1", "username": "john", "phone": "", "is_active": true, "is_staff": false, "is_superuser": true, "is_ldap_user": true, "num_groups": float64(2)},
			wantChanges: map[string]any{},
		},
		{
			name:        "only the edited field is sent",
			edited:      map[string]any{"phone": "010", "is_staff": false, "is_ldap_user": true},
			wantChanges: map[string]any{"phone": "010"},
		},
		{
			name:           "a privilege edit is held back, not sent",
			edited:         map[string]any{"is_staff": true},
			wantChanges:    map[string]any{},
			wantPrivileges: []PrivilegeEdit{{Field: "is_staff", Enable: true}},
		},
		{
			name:           "a mixed edit applies the rest and reports the flag",
			edited:         map[string]any{"phone": "010", "is_staff": true},
			wantChanges:    map[string]any{"phone": "010"},
			wantPrivileges: []PrivilegeEdit{{Field: "is_staff", Enable: true}},
		},
		{
			name:           "clearing a flag is reported the same way",
			edited:         map[string]any{"is_superuser": false},
			wantChanges:    map[string]any{},
			wantPrivileges: []PrivilegeEdit{{Field: "is_superuser", Enable: false}},
		},
		{
			name:           "a flag left at its current value is not reported",
			edited:         map[string]any{"is_superuser": true, "is_staff": true},
			wantChanges:    map[string]any{},
			wantPrivileges: []PrivilegeEdit{{Field: "is_staff", Enable: true}},
		},
		{
			name:        "a deleted key reads as untouched",
			edited:      map[string]any{"username": "john"},
			wantChanges: map[string]any{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			edit, err := diffEditedUser(original, tt.edited)
			require.NoError(t, err)

			assert.Equal(t, tt.wantChanges, edit.Changes)
			assert.Equal(t, tt.wantPrivileges, edit.Privileges)
		})
	}
}

// Re-submitting an untouched is_ldap_user makes the server run a live LDAP bind, so an
// unreachable directory would fail an edit that never touched the field.
func TestDiffEditedUser_DoesNotResubmitUntouchedLDAPFlag(t *testing.T) {
	original := []byte(`{"phone": "", "is_ldap_user": true}`)

	edit, err := diffEditedUser(original, map[string]any{"phone": "010", "is_ldap_user": true})
	require.NoError(t, err)

	assert.NotContains(t, edit.Changes, "is_ldap_user")
	assert.Equal(t, map[string]any{"phone": "010"}, edit.Changes)
}

func TestDiffEditedUser_RejectsMalformedInput(t *testing.T) {
	tests := []struct {
		name   string
		edited any
		errIs  string
	}{
		{"a JSON array is not a user", []any{}, "JSON object"},
		{"a non-boolean flag", map[string]any{"is_staff": "true"}, "is_staff must be true or false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := diffEditedUser([]byte(`{"is_staff": false}`), tt.edited)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errIs)
		})
	}
}
