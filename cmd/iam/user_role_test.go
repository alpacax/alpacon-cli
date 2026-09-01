package iam

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/rbac"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func workspaceBinding(id, roleName string) rbac.UserRoleResponse {
	return rbac.UserRoleResponse{ID: id, Role: rbac.RoleNested{ID: id + "-role", Name: roleName}}
}

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

func TestRoleCommandFor(t *testing.T) {
	tests := []struct {
		name      string
		privilege iam.PrivilegeEdit
		want      string
	}{
		{"setting is_staff grants admin", iam.PrivilegeEdit{Field: "is_staff", Enable: true}, "alpacon user role grant john admin"},
		{"clearing is_staff revokes admin", iam.PrivilegeEdit{Field: "is_staff", Enable: false}, "alpacon user role revoke john admin"},
		{"setting is_superuser grants superuser", iam.PrivilegeEdit{Field: "is_superuser", Enable: true}, "alpacon user role grant john superuser"},
		{"clearing is_superuser revokes superuser", iam.PrivilegeEdit{Field: "is_superuser", Enable: false}, "alpacon user role revoke john superuser"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, roleCommandFor("john", tt.privilege).Command)
		})
	}
}

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

// Within 'alpacon user' the RBAC role is always positional, so --role means only the
// group-membership tier. The walk cannot start at RootCmd (cmd imports cmd/iam, not the
// reverse), so GroupCmd's --role is asserted here as the one intended holder.
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

type statusOnlyError struct{ status int }

func (e statusOnlyError) Error() string       { return "forbidden" }
func (e statusOnlyError) HTTPStatusCode() int { return e.status }

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
			name: "a role read with an api token leads with visibility, not the credential",
			ac:   apiToken, gate: gateRoleRead,
			wantSaid:    []string{"may not see", "API token", "alpacon login"},
			wantNotSaid: []string{"superuser role", "role_audit_log:read"},
		},
		{
			// The auditor limit narrows silently and never 403s, so the scope is the only
			// cause this arm can name.
			name: "the audit log names the missing scope, not the auditor limit",
			ac:   apiToken, gate: gateAuditRead,
			wantSaid:    []string{"role_audit_log:read"},
			wantNotSaid: []string{"superuser role", "auditor"},
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

func TestDescribeRBACError_CodedRefusalIgnoresTheGate(t *testing.T) {
	ac := &client.AlpaconClient{Token: "alpat-token"}
	// The JSON envelope: ParseErrorResponse reads "code: X" only when the prefix starts
	// its own "; " segment, which a status-prefixed message never does.
	coded := fmt.Errorf("request failed with status 400: {\"code\": %q}", codeSuperuserLastRemoval)

	for _, gate := range []rbacGate{gateRoleRead, gateRoleWrite, gateAuditRead, gateUserRead, gatePermissionIntrospect} {
		got := describeRBACError(ac, gate, coded)
		require.Error(t, got)
		assert.Contains(t, got.Error(), "last superuser")
	}
}

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
		{"a canonical uuid passes through unchanged", []string{otherID}, otherID, otherID, otherID},
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

// A coded 403 never reaches the codeless branch, so it needs a case of its own.
func TestDescribeRBACError_MapsPermissionDenied(t *testing.T) {
	ac := &client.AlpaconClient{AccessToken: "bearer-token"}
	coded := fmt.Errorf("request failed with status 403: {\"code\": %q}", codePermissionDenied)

	got := describeRBACError(ac, gateRoleRead, coded)
	require.Error(t, got)
	assert.Contains(t, got.Error(), "not an account you may read")
}

func TestDescribeRBACError_KeepsTheCauseInTheChain(t *testing.T) {
	ac := &client.AlpaconClient{AccessToken: "bearer-token"}
	cause := statusOnlyError{status: http.StatusForbidden}

	got := describeRBACError(ac, gateRoleWrite, cause)
	require.Error(t, got)

	assert.NotContains(t, got.Error(), "forbidden", "the raw refusal must not pad the operator's line")
	assert.Equal(t, http.StatusForbidden, utils.HTTPStatusCode(got))

	// The code has to survive the rewrite too. Nothing consumes it downstream today -
	// HandleCommonErrors and IsDuplicateBinding both see the raw error and the rewrite
	// runs after them - so this pins a forward contract, not a live path.
	coded := fmt.Errorf("request failed with status 400: {\"code\": %q}", codeSuperuserLastRemoval)
	code, _ := utils.ParseErrorResponse(describeRBACError(ac, gateRoleWrite, coded))
	assert.Equal(t, codeSuperuserLastRemoval, code)
}

func TestResolveSubjectCanonicalizesAUUID(t *testing.T) {
	const canonical = "2222222a-2222-2222-2222-22222222222b"

	for _, typed := range []string{
		canonical,
		"2222222A-2222-2222-2222-22222222222B",
		"2222222a222222222222222222222 22b",
	} {
		typed := strings.ReplaceAll(typed, " ", "")
		t.Run(typed, func(t *testing.T) {
			require.True(t, utils.IsUUID(typed), "the fast path only fires for what IsUUID accepts: %q", typed)

			got := resolveSubject(nil, []string{typed})
			assert.Equal(t, canonical, got.PK)
			assert.Equal(t, canonical, got.ID)
			assert.Equal(t, typed, got.Label, "the label echoes what the operator typed")
		})
	}
}
