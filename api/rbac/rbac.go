package rbac

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	rolesURL         = "/api/rbac/roles/"
	userRolesURL     = "/api/rbac/user-roles/"
	roleAuditLogsURL = "/api/rbac/role-audit-logs/"
	troubleshootURL  = "/api/rbac/troubleshoot/"

	// usersURL is the IAM user endpoint, which carries the RBAC introspection
	// actions: a user's effective permissions hang off the principal rather than off
	// /api/rbac/.
	usersURL = "/api/iam/users/"

	// timeLayout matches the other list projections in api/.
	timeLayout = "2006-01-02 15:04"

	// RoleAdmin and RoleSuperuser are the two platform tiers: the global roles that
	// say what an account is rather than what it may do. The server mirrors them onto
	// the legacy is_staff and is_superuser flags, refuses to let a workspace run out
	// of holders of either, and creates the admin companion itself whenever superuser
	// is granted.
	RoleAdmin     = "admin"
	RoleSuperuser = "superuser"

	// CodeRoleAssignmentDuplicate is the server's refusal of a grant the user already
	// holds at the same scope. Exported because both the grant path, which absorbs it,
	// and the error rewriting in cmd/ have to name it—and a wire string with two copies
	// is a wire string with one stale copy.
	CodeRoleAssignmentDuplicate = "rbac_role_assignment_duplicate"
)

// GetRoleCatalog lists the workspace's roles. When autoAssigned is non-nil the
// server's auto_assigned filter is sent; nil sends none.
//
// The filter is a suffix match on ':owner', ':master', ':member' and ':manager',
// not a judgement about whether a role is worth granting—it hides
// service_token:manager, which is granted by hand, and keeps member, which every
// account holds. Callers should describe it as hiding the object-scoped plumbing
// roles rather than as listing the assignable ones.
func GetRoleCatalog(ac *client.AlpaconClient, autoAssigned *bool) ([]RoleResponse, error) {
	var params map[string]string
	if autoAssigned != nil {
		params = map[string]string{"auto_assigned": strconv.FormatBool(*autoAssigned)}
	}

	return api.FetchAllPages[RoleResponse](ac, rolesURL, params)
}

// ResolveRole turns a role name or UUID into the role itself.
//
// The server's name filter is an exact, case-sensitive match, and both the role
// list and the binding list are narrowed to what the caller can see rather than
// refused—so a non-admin asking about a role they do not hold gets an empty page,
// never a 403. The not-found message has to cover both readings.
func ResolveRole(ac *client.AlpaconClient, nameOrID string) (*RoleResponse, error) {
	if utils.IsUUID(nameOrID) {
		responseBody, err := ac.SendGetRequest(utils.BuildURL(rolesURL, nameOrID, nil))
		if err != nil {
			return nil, err
		}

		var role RoleResponse
		if err = json.Unmarshal(responseBody, &role); err != nil {
			return nil, err
		}
		return &role, nil
	}

	roles, err := api.FetchAllPages[RoleResponse](ac, rolesURL, map[string]string{"name": nameOrID})
	if err != nil {
		return nil, err
	}
	if len(roles) == 0 {
		return nil, fmt.Errorf("no role named %q is visible to you; the name is matched exactly and is case-sensitive", nameOrID)
	}

	return &roles[0], nil
}

// GetRoleScopes reports what a role grants.
func GetRoleScopes(ac *client.AlpaconClient, roleID string) (*RoleScopesResponse, error) {
	responseBody, err := ac.SendGetRequest(utils.BuildURL(rolesURL, roleID+"/scopes", nil))
	if err != nil {
		return nil, err
	}

	var scopes RoleScopesResponse
	if err = json.Unmarshal(responseBody, &scopes); err != nil {
		return nil, err
	}

	return &scopes, nil
}

// GetUserBindings lists the role bindings a user holds, at every scope tier.
//
// The content_type filter is deliberately not sent: the server offers no isnull
// lookup and no object_id filter, so the workspace-wide row is selected here rather
// than asked for.
//
// The result is what the caller may see, not necessarily the whole truth. A
// non-admin is narrowed to their own bindings and answered 200, never 403, so an
// empty page reads the same as "this user holds nothing".
func GetUserBindings(ac *client.AlpaconClient, userID string) ([]UserRoleResponse, error) {
	return api.FetchAllPages[UserRoleResponse](ac, userRolesURL, map[string]string{"user": userID})
}

// GetRoleHolders lists the holders of a role, at every scope tier — subject to the
// same narrowing as GetUserBindings, so a non-admin sees only their own binding and
// is not told the list was cut.
func GetRoleHolders(ac *client.AlpaconClient, roleID string) ([]UserRoleResponse, error) {
	return api.FetchAllPages[UserRoleResponse](ac, userRolesURL, map[string]string{"role": roleID})
}

// GetRoleHistory reports the grants and revocations recorded against a user,
// newest first. This is the only surface carrying the justification and the actor;
// a binding read carries neither.
func GetRoleHistory(ac *client.AlpaconClient, userID string, limit int) ([]RoleAuditLogResponse, error) {
	params := map[string]string{"user": userID}

	// No ordering parameter: the view pins ordering to '-added_at, -id' and exposes
	// no ordering_fields, so the newest rows already come first and anything sent
	// here would be dropped.
	return api.FetchPagesUpTo[RoleAuditLogResponse](ac, roleAuditLogsURL, params, limit)
}

// FindWorkspaceBinding returns the caller's workspace-wide binding for roleID, or
// nil. At most one can exist: a partial unique constraint covers the rows whose
// scope columns are both null.
func FindWorkspaceBinding(bindings []UserRoleResponse, roleID string) *UserRoleResponse {
	for i := range bindings {
		if bindings[i].Role.ID == roleID && bindings[i].IsWorkspaceWide() {
			return &bindings[i]
		}
	}

	return nil
}

// FindWorkspaceBindingByName is FindWorkspaceBinding for a caller holding the role's
// name rather than its id. The nested role object on a binding carries the name, so
// this costs no extra request—which is what lets a revoke find the admin companion
// without resolving a second role.
func FindWorkspaceBindingByName(bindings []UserRoleResponse, roleName string) *UserRoleResponse {
	for i := range bindings {
		if bindings[i].Role.Name == roleName && bindings[i].IsWorkspaceWide() {
			return &bindings[i]
		}
	}

	return nil
}

// HoldsWorkspaceRole reports whether the bindings include a workspace-wide grant of
// the named role.
func HoldsWorkspaceRole(bindings []UserRoleResponse, roleName string) bool {
	return FindWorkspaceBindingByName(bindings, roleName) != nil
}

// IsPlatformTier reports whether roleName is one of the two tiers. Their grants are
// what a confirmation prompt and a missing justification are worth warning about;
// the capability roles are ordinary.
func IsPlatformTier(roleName string) bool {
	return roleName == RoleAdmin || roleName == RoleSuperuser
}

// WorkspaceRoleNames lists, sorted, the workspace-wide roles the bindings carry. It
// is what a write reports after re-reading, so the admin companion the server
// created for a superuser grant is observed rather than assumed.
func WorkspaceRoleNames(bindings []UserRoleResponse) []string {
	var names []string
	for _, binding := range bindings {
		if binding.IsWorkspaceWide() {
			names = append(names, binding.Role.Name)
		}
	}
	sort.Strings(names)

	return names
}

func RoleAttributesFrom(roles []RoleResponse) []RoleAttributes {
	var result []RoleAttributes
	for _, role := range roles {
		result = append(result, RoleAttributes{
			Name:        role.Name,
			Description: role.Description,
		})
	}

	return result
}

func BindingAttributesFrom(bindings []UserRoleResponse) []UserRoleAttributes {
	var result []UserRoleAttributes
	for _, binding := range bindings {
		result = append(result, UserRoleAttributes{
			Role:      binding.Role.Name,
			Scope:     binding.ScopeLabel(),
			GrantedAt: binding.AddedAt.Local().Format(timeLayout),
		})
	}

	return result
}

// HolderAttributesFrom projects a role's holders.
//
// A nil usernames map means the caller chose not to resolve names, and the column
// then carries the user id—the one value another command always accepts. A non-nil
// map resolves, falling back to the nested display name only for a holder the map
// does not contain. The distinction matters: the nested user object carries a
// display name, so falling back to it when resolution was skipped would print
// something no other command takes.
func HolderAttributesFrom(bindings []UserRoleResponse, usernames map[string]string) []RoleHolderAttributes {
	var result []RoleHolderAttributes
	for _, binding := range bindings {
		user := binding.User.ID
		if usernames != nil {
			if resolved := usernames[binding.User.ID]; resolved != "" {
				user = resolved
			} else if binding.User.Name != "" {
				user = binding.User.Name
			}
		}

		result = append(result, RoleHolderAttributes{
			User:      user,
			Scope:     binding.ScopeLabel(),
			GrantedAt: binding.AddedAt.Local().Format(timeLayout),
		})
	}

	return result
}

// ScopeAttributesFrom flattens a role's permissions for the table. Wildcards come
// first: they subsume the concrete rows below them.
func ScopeAttributesFrom(scopes *RoleScopesResponse) []RoleScopeAttributes {
	var result []RoleScopeAttributes
	for _, wildcard := range scopes.Wildcards {
		result = append(result, RoleScopeAttributes{Name: wildcard})
	}
	for _, resource := range scopes.Resources {
		result = append(result, RoleScopeAttributes{
			Name:    resource.Name,
			Actions: strings.Join(resource.Actions, ", "),
			ACL:     strings.Join(resource.ACL, ", "),
		})
	}

	return result
}

func AuditAttributesFrom(entries []RoleAuditLogResponse) []RoleAuditAttributes {
	var result []RoleAuditAttributes
	for _, entry := range entries {
		changedBy := ""
		if entry.ChangedBy != nil {
			changedBy = entry.ChangedBy.Label
		}

		result = append(result, RoleAuditAttributes{
			Action:    entry.Action,
			Role:      entry.RoleName,
			Scope:     entry.Scope,
			ChangedBy: changedBy,
			Reason:    entry.Reason,
			At:        entry.AddedAt.Local().Format(timeLayout),
		})
	}

	return result
}

// GrantRole binds a role to a user. The request carries scalars, which is what
// keeps the server on the single-row path: the bulk path answers 201 with an empty
// body whether or not it created anything, and only the single-row path reports a
// duplicate.
func GrantRole(ac *client.AlpaconClient, request BindingCreateRequest) (*UserRoleResponse, error) {
	responseBody, err := ac.SendPostRequest(userRolesURL, request)
	if err != nil {
		return nil, err
	}

	var binding UserRoleResponse
	if err = json.Unmarshal(responseBody, &binding); err != nil {
		return nil, err
	}

	return &binding, nil
}

// RevokeRole deletes one binding by its own id. Bindings carry no update path, so
// this is the only way a grant is taken back.
func RevokeRole(ac *client.AlpaconClient, bindingID, reason string) error {
	url := utils.BuildURL(userRolesURL, bindingID, nil)

	if reason == "" {
		_, err := ac.SendDeleteRequest(url)
		return err
	}

	_, err := ac.SendDeleteRequestWithBody(url, BindingRevokeRequest{Reason: reason})
	return err
}

// IsDuplicateBinding reports whether err is the server refusing a grant the user
// already holds. A grant that finds the binding already there has converged, so
// this is success rather than failure.
func IsDuplicateBinding(err error) bool {
	code, _ := utils.ParseErrorResponse(err)
	return code == CodeRoleAssignmentDuplicate
}

// GetEffectivePermissions reports where a user's authority comes from and what it
// adds up to. It lives on the user endpoint rather than under /api/rbac/, so it
// answers for an API token on workspaces where the RBAC routes do not.
func GetEffectivePermissions(ac *client.AlpaconClient, userID string) (*EffectivePermissionsResponse, error) {
	responseBody, err := ac.SendGetRequest(utils.BuildURL(usersURL, userID+"/effective-permissions", nil))
	if err != nil {
		return nil, err
	}

	var effective EffectivePermissionsResponse
	if err = json.Unmarshal(responseBody, &effective); err != nil {
		return nil, err
	}

	return &effective, nil
}

// GetPermissionPatterns lists the raw permission patterns a user holds, split by
// how widely they were granted.
func GetPermissionPatterns(ac *client.AlpaconClient, userID string) (*PermissionPatternsResponse, error) {
	responseBody, err := ac.SendGetRequest(utils.BuildURL(usersURL, userID+"/permissions", nil))
	if err != nil {
		return nil, err
	}

	var patterns PermissionPatternsResponse
	if err = json.Unmarshal(responseBody, &patterns); err != nil {
		return nil, err
	}

	return &patterns, nil
}

// CheckPermission answers whether one permission is allowed workspace-wide.
//
// The question is the global one: a permission the user holds only through an
// object-scoped binding answers false here, because no object was named.
func CheckPermission(ac *client.AlpaconClient, userID, permission string) (bool, error) {
	params := map[string]string{"permission": permission}

	responseBody, err := ac.SendGetRequest(utils.BuildURL(usersURL, userID+"/permissions", params))
	if err != nil {
		return false, err
	}

	var check PermissionCheckResponse
	if err = json.Unmarshal(responseBody, &check); err != nil {
		return false, err
	}

	return check.Allowed, nil
}

// ExplainPermission returns the server's account of how a permission decision was
// reached, as raw JSON for the caller to print.
func ExplainPermission(ac *client.AlpaconClient, userID, permission string) ([]byte, error) {
	return ac.SendPostRequest(troubleshootURL, TroubleshootRequest{UserID: userID, Permission: permission})
}

func EffectiveRoleAttributesFrom(roles []EffectiveRole) []EffectiveRoleAttributes {
	var result []EffectiveRoleAttributes
	for _, role := range roles {
		group := ""
		if role.Group != nil {
			group = role.Group.Name
		}

		result = append(result, EffectiveRoleAttributes{
			Role:   role.Role.Name,
			Source: role.Source,
			Group:  group,
			Scope:  role.Scope,
		})
	}

	return result
}

func PermissionPatternAttributesFrom(patterns *PermissionPatternsResponse) []PermissionPatternAttributes {
	var result []PermissionPatternAttributes
	for _, pattern := range patterns.Global {
		result = append(result, PermissionPatternAttributes{Permission: pattern, Scope: "workspace"})
	}
	for _, pattern := range patterns.ObjectScoped {
		result = append(result, PermissionPatternAttributes{Permission: pattern, Scope: "object-scoped"})
	}

	return result
}
