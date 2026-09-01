package iam

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/rbac"
	"github.com/alpacax/alpacon-cli/client"
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

// The warning judges the trimmed value, so the wire has to carry the trimmed value
// too. A whitespace-only justification sent as-is would warn the operator that the
// audit entry carries nothing while filling it with blanks, which also keeps the
// server's own unjustified-grant warning quiet.
func TestReasonFlagTrimsOnce(t *testing.T) {
	tests := []struct {
		name string
		flag string
		want string
	}{
		{"unset", "", ""},
		{"whitespace only", "   \t ", ""},
		{"padded", "  SEC-1421  ", "SEC-1421"},
		{"already clean", "on-call rotation Q3", "on-call rotation Q3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "x"}
			cmd.Flags().String("reason", "", "")
			require.NoError(t, cmd.Flags().Set("reason", tt.flag))

			assert.Equal(t, tt.want, reasonFlag(cmd))
		})
	}
}

// statusOnlyError is a 403 carrying no error code, which is what the authority and
// credential gates actually answer. utils.HTTPStatusCode walks the chain for this
// interface, so a local type is enough to drive the rewrite.
type statusOnlyError struct{ status int }

func (e statusOnlyError) Error() string       { return "forbidden" }
func (e statusOnlyError) HTTPStatusCode() int { return e.status }

// The codeless 403 is the refusal an operator is most likely to hit, and the three
// surfaces fail for three different reasons. Naming the wrong one sends them after a
// fix that cannot work - re-running 'alpacon login' does not grant a superuser role,
// and holding one does not make an Alpacon Cloud workspace accept an API token.
func TestDescribeRBACError_CodelessForbidden(t *testing.T) {
	bearer := &client.AlpaconClient{AccessToken: "bearer-token"}
	apiToken := &client.AlpaconClient{Token: "alpat-token"}

	tests := []struct {
		name        string
		ac          *client.AlpaconClient
		gate        rbacGate
		wantSaid    []string
		wantNotSaid []string
	}{
		{
			name: "a write with a bearer names only the superuser role",
			ac:   bearer, gate: gateRoleWrite,
			wantSaid:    []string{"superuser role"},
			wantNotSaid: []string{"alpacon login", "API token"},
		},
		{
			name: "a write with an api token names both, superuser first",
			ac:   apiToken, gate: gateRoleWrite,
			wantSaid: []string{"superuser role", "API token", "alpacon login"},
		},
		{
			// One message per gate, so neither has to walk back what it just said: this
			// one refuses the token outright and never mentions a scope.
			name: "a role read with an api token never mentions a write privilege or a scope",
			ac:   apiToken, gate: gateRoleRead,
			wantSaid:    []string{"API token", "alpacon login"},
			wantNotSaid: []string{"superuser role", "write", "role_audit_log:read", "exception"},
		},
		{
			name: "the audit log names the scope, not the token refusal",
			ac:   apiToken, gate: gateAuditRead,
			wantSaid:    []string{"role_audit_log:read"},
			wantNotSaid: []string{"superuser role", "refuses API tokens"},
		},
		{
			name: "the audit log with a bearer names the audit reach",
			ac:   bearer, gate: gateAuditRead,
			wantSaid:    []string{"auditor"},
			wantNotSaid: []string{"superuser role", "role_audit_log:read"},
		},
		{
			name: "a role read with a bearer says visibility, not privilege",
			ac:   bearer, gate: gateRoleRead,
			wantSaid:    []string{"may not see"},
			wantNotSaid: []string{"superuser role", "alpacon login"},
		},
		{
			name: "the effective-permissions read names user:read, whatever the credential",
			ac:   apiToken, gate: gateUserRead,
			wantSaid:    []string{"user:read"},
			wantNotSaid: []string{"superuser role", "alpacon login"},
		},
		{
			// /permissions/ pins no scope, so telling the caller to get user:read would
			// send a workspace admin after a permission they already hold.
			name: "the permission introspection read does not name user:read",
			ac:   apiToken, gate: gatePermissionIntrospect,
			wantSaid:    []string{"wildcard", "superuser role", "without a USER argument"},
			wantNotSaid: []string{"user:read", "alpacon login"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := describeRBACError(tt.ac, tt.gate, statusOnlyError{status: http.StatusForbidden})
			require.Error(t, got)

			for _, want := range tt.wantSaid {
				assert.Contains(t, got.Error(), want)
			}
			for _, unwanted := range tt.wantNotSaid {
				assert.NotContains(t, got.Error(), unwanted)
			}
		})
	}
}

// A coded refusal is the server stating what it wants, so the gate must not change
// the message - the code already carries the cause.
func TestDescribeRBACError_CodedRefusalIgnoresTheGate(t *testing.T) {
	ac := &client.AlpaconClient{Token: "alpat-token"}
	// The server's own envelope, which is what ParseErrorResponse reads: a prefixed
	// "code: X" is not parsed, because the prefix has to start its own segment.
	coded := fmt.Errorf("request failed with status 400: {\"code\": %q}", codeSuperuserLastRemoval)

	for _, gate := range []rbacGate{gateRoleRead, gateRoleWrite, gateAuditRead, gateUserRead, gatePermissionIntrospect} {
		got := describeRBACError(ac, gate, coded)
		require.Error(t, got)
		assert.Contains(t, got.Error(), "last superuser")
	}
}

// Refusing a revoke the workspace has nothing to revoke would break this command's
// own promise that revoking an unheld role succeeds. The invariant only bites when
// there is a row to delete.
func TestWouldStrandThePlatformFlags(t *testing.T) {
	adminRow := workspaceBinding("adm", rbac.RoleAdmin)
	superRow := workspaceBinding("su", rbac.RoleSuperuser)

	tests := []struct {
		name     string
		bindings []rbac.UserRoleResponse
		roleName string
		targets  []rbac.UserRoleResponse
		want     bool
	}{
		{
			name:     "admin while superuser stands is the half-state to refuse",
			bindings: []rbac.UserRoleResponse{adminRow, superRow},
			roleName: rbac.RoleAdmin,
			targets:  []rbac.UserRoleResponse{adminRow},
			want:     true,
		},
		{
			name:     "superuser held but no admin row leaves nothing to strand",
			bindings: []rbac.UserRoleResponse{superRow},
			roleName: rbac.RoleAdmin,
			targets:  nil,
			want:     false,
		},
		{
			name:     "admin alone is an ordinary revoke",
			bindings: []rbac.UserRoleResponse{adminRow},
			roleName: rbac.RoleAdmin,
			targets:  []rbac.UserRoleResponse{adminRow},
			want:     false,
		},
		{
			name:     "revoking superuser is never the stranding case",
			bindings: []rbac.UserRoleResponse{adminRow, superRow},
			roleName: rbac.RoleSuperuser,
			targets:  []rbac.UserRoleResponse{superRow},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, wouldStrandThePlatformFlags(tt.bindings, tt.roleName, tt.targets))
		})
	}
}

// The self form must address the IAM user routes by the "-" alias, not by the
// caller's UUID. The alias is what makes UserViewSet.get_object skip the object
// permission check, and that check is what refuses an operator their own
// permissions: /permissions/ pins no scope so a UUID resolves to an orphan
// 'user:permissions', and /effective-permissions/ pins user:read, which the baseline
// member role does not carry.
func TestResolveSubject(t *testing.T) {
	const (
		callerID = "11111111-1111-1111-1111-111111111111"
		otherID  = "33333333-3333-3333-3333-333333333333"
	)

	tests := []struct {
		name      string
		args      []string
		wantPK    string
		wantID    string
		wantLabel string
	}{
		{"no argument addresses the caller by alias", nil, "-", callerID, "root"},
		{"a username resolves to its uuid", []string{"john"}, otherID, otherID, "john"},
		{"a uuid is taken as given", []string{otherID}, otherID, otherID, otherID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/iam/users/-/" {
					_ = json.NewEncoder(w).Encode(map[string]any{"id": callerID, "username": "root"})
					return
				}
				_ = json.NewEncoder(w).Encode(api.ListResponse[map[string]any]{
					Count:   1,
					Results: []map[string]any{{"id": otherID, "username": "john"}},
				})
			}))
			defer ts.Close()

			got := resolveSubject(&client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}, tt.args)
			assert.Equal(t, tt.wantPK, got.PK)
			assert.Equal(t, tt.wantID, got.ID)
			assert.Equal(t, tt.wantLabel, got.Label)
		})
	}
}
