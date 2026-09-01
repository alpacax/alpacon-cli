package iam

import (
	"testing"

	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/rbac"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func workspaceBinding(id, roleName string) rbac.UserRoleResponse {
	return rbac.UserRoleResponse{ID: id, Role: rbac.RoleNested{ID: id + "-role", Name: roleName}}
}

// Superuser first is not a preference. Deleting admin first would leave the
// superuser binding standing over an account that no longer registers as an admin,
// and an interruption there fails open.
func TestPlannedRevocations(t *testing.T) {
	contentType := 42
	scopedAdmin := rbac.UserRoleResponse{ID: "scoped", Role: rbac.RoleNested{Name: rbac.RoleAdmin}, ContentType: &contentType}

	tests := []struct {
		name     string
		bindings []rbac.UserRoleResponse
		roleName string
		cascade  bool
		wantIDs  []string
	}{
		{
			name:     "a plain revoke deletes one binding",
			bindings: []rbac.UserRoleResponse{workspaceBinding("su", rbac.RoleSuperuser), workspaceBinding("adm", rbac.RoleAdmin)},
			roleName: rbac.RoleSuperuser,
			wantIDs:  []string{"su"},
		},
		{
			name:     "a cascade deletes superuser first, then the companion",
			bindings: []rbac.UserRoleResponse{workspaceBinding("su", rbac.RoleSuperuser), workspaceBinding("adm", rbac.RoleAdmin)},
			roleName: rbac.RoleSuperuser,
			cascade:  true,
			wantIDs:  []string{"su", "adm"},
		},
		{
			// The regression that matters: a plain admin has the same binding list as a
			// user whose cascade was interrupted, so treating this as a resume would
			// strip administrator access from someone who was never a superuser.
			name:     "a cascade plans nothing when the named binding is absent",
			bindings: []rbac.UserRoleResponse{workspaceBinding("adm", rbac.RoleAdmin)},
			roleName: rbac.RoleSuperuser,
			cascade:  true,
			wantIDs:  nil,
		},
		{
			name:     "a cascade over a user holding neither plans nothing",
			bindings: nil,
			roleName: rbac.RoleSuperuser,
			cascade:  true,
			wantIDs:  nil,
		},
		{
			name:     "a role the user does not hold plans nothing",
			bindings: []rbac.UserRoleResponse{workspaceBinding("adm", rbac.RoleAdmin)},
			roleName: "operator",
			wantIDs:  nil,
		},
		{
			name:     "an object-scoped binding of the same role is never a target",
			bindings: []rbac.UserRoleResponse{scopedAdmin},
			roleName: rbac.RoleAdmin,
			wantIDs:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targets := plannedRevocations(tt.bindings, tt.roleName, tt.cascade)

			ids := make([]string, 0, len(targets))
			for _, target := range targets {
				ids = append(ids, target.ID)
			}
			if tt.wantIDs == nil {
				assert.Empty(t, ids)
				return
			}
			assert.Equal(t, tt.wantIDs, ids)
		})
	}
}

func TestDescribeTargets(t *testing.T) {
	assert.Equal(t, "superuser", describeTargets([]rbac.UserRoleResponse{workspaceBinding("su", rbac.RoleSuperuser)}))
	assert.Equal(t, "superuser and admin", describeTargets([]rbac.UserRoleResponse{
		workspaceBinding("su", rbac.RoleSuperuser),
		workspaceBinding("adm", rbac.RoleAdmin),
	}))
}

// The command an operator is sent to must perform what their editor edit asked for,
// including the direction: setting a flag grants, clearing it revokes.
func TestRoleCommandFor(t *testing.T) {
	tests := []struct {
		name      string
		privilege iam.PrivilegeEdit
		want      string
	}{
		{"setting is_staff grants admin", iam.PrivilegeEdit{Field: "is_staff", Want: true}, "alpacon user role grant john admin"},
		{"clearing is_staff revokes admin", iam.PrivilegeEdit{Field: "is_staff", Want: false}, "alpacon user role revoke john admin"},
		{"setting is_superuser grants superuser", iam.PrivilegeEdit{Field: "is_superuser", Want: true}, "alpacon user role grant john superuser"},
		{"clearing is_superuser revokes superuser", iam.PrivilegeEdit{Field: "is_superuser", Want: false}, "alpacon user role revoke john superuser"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, roleCommandFor("john", tt.privilege).Command)
		})
	}
}

// One argument is the permission and the subject is the caller; two are the user
// then the permission. A username cannot contain a colon, which is what makes the
// one-argument form unambiguous.
func TestSplitCanIArgs(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		wantSubject    []string
		wantPermission string
	}{
		{"one argument asks about yourself", []string{"server:update"}, nil, "server:update"},
		{"two arguments name the subject", []string{"john", "server:update"}, []string{"john"}, "server:update"},
		{"a wildcard is a permission", []string{"john", "server:*"}, []string{"john"}, "server:*"},
		{"a bare wildcard is a permission", []string{"*"}, nil, "*"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subject, permission := splitCanIArgs(tt.args)
			assert.Equal(t, tt.wantSubject, subject)
			assert.Equal(t, tt.wantPermission, permission)
		})
	}
}

// The commands must stay registered under 'alpacon user', because that is the only
// place an operator looking for them will think to check.
func TestUserSubcommandsAreRegistered(t *testing.T) {
	for _, path := range [][]string{
		{"role", "ls"},
		{"role", "catalog"},
		{"role", "describe"},
		{"role", "grant"},
		{"role", "revoke"},
		{"role", "history"},
		{"permission", "ls"},
		{"permission", "can-i"},
	} {
		cmd, _, err := UserCmd.Find(path)
		require.NoError(t, err)
		assert.Equal(t, path[len(path)-1], cmd.Name())
	}
}

// Within 'alpacon user', --role means the group-membership tier and nothing else:
// the RBAC role is always a positional. The walk covers every subcommand rather
// than a list, so a leaf added later is held to the same rule. It cannot reach
// RootCmd—cmd imports cmd/iam, not the other way round—so 'alpacon group member
// add --role' is asserted here directly as the one intended holder of the name.
func TestRoleFlagKeepsOneMeaning(t *testing.T) {
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		assert.Nil(t, cmd.Flags().Lookup("role"),
			"%q must take the RBAC role as a positional, never as a --role flag", cmd.CommandPath())
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(UserCmd)

	memberAdd, _, err := GroupCmd.Find([]string{"member", "add"})
	require.NoError(t, err)
	assert.NotNil(t, memberAdd.Flags().Lookup("role"), "group membership is where --role belongs")
}

// A permission carries a colon or a wildcard; a username carries neither. One rule
// decides both argument forms, so the same string is accepted or refused wherever it
// is typed—and a bare '*' stays a legal question.
func TestLooksLikePermission(t *testing.T) {
	tests := []struct {
		arg  string
		want bool
	}{
		{"server:update", true},
		{"server:*", true},
		{"*:read", true},
		{"*", true},
		{"john", false},
		{"server-update", false},
		{"john_doe", false},
	}

	for _, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			assert.Equal(t, tt.want, looksLikePermission(tt.arg))
		})
	}
}
