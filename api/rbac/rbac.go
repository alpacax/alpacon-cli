package rbac

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	rolesURL         = "/api/rbac/roles/"
	userRolesURL     = "/api/rbac/user-roles/"
	roleAuditLogsURL = "/api/rbac/role-audit-logs/"
	troubleshootURL  = "/api/rbac/troubleshoot/"

	usersURL = "/api/iam/users/"

	timeLayout = "2006-01-02 15:04"

	// Platform tiers: the server mirrors them onto the legacy is_staff/is_superuser flags,
	// creates the admin companion when superuser is granted, and refuses to leave a workspace
	// without a holder of either.
	RoleAdmin     = "admin"
	RoleSuperuser = "superuser"

	CodeRoleAssignmentDuplicate = "rbac_role_assignment_duplicate"
)

// auto_assigned is a name-suffix match on ':owner', ':master', ':member' and
// ':manager', not a judgement of assignability: true returns nothing but the
// object-scoped plumbing roles, and false also hides hand-granted service_token:manager.
func GetRoleCatalog(ac *client.AlpaconClient, autoAssigned *bool) ([]RoleResponse, error) {
	var params map[string]string
	if autoAssigned != nil {
		params = map[string]string{"auto_assigned": strconv.FormatBool(*autoAssigned)}
	}

	return api.FetchAllPages[RoleResponse](ac, rolesURL, params)
}

// The name filter is exact and case-sensitive. Role lists are narrowed to what the caller
// may see rather than refused, so an invisible role yields an empty page and never a 403—the
// not-found message covers both readings.
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

	// The name filter is exact, so a second round trip would be wasted.
	roles, err := api.FetchPagesUpTo[RoleResponse](ac, rolesURL, map[string]string{"name": nameOrID}, 1)
	if err != nil {
		return nil, err
	}
	if len(roles) == 0 {
		return nil, fmt.Errorf("no role named %q is visible to you; the name is matched exactly and is case-sensitive", nameOrID)
	}

	return &roles[0], nil
}

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

// The endpoint offers no scope filter, so the workspace-wide row is picked out here. The
// list is also narrowed to what the caller may see and answered 200, never 403, so an empty
// result reads exactly like "holds nothing"—no caller may take it as proof of absence.
func GetUserBindings(ac *client.AlpaconClient, userID string) ([]UserRoleResponse, error) {
	return api.FetchAllPages[UserRoleResponse](ac, userRolesURL, map[string]string{"user": userID})
}

func GetRoleHolders(ac *client.AlpaconClient, roleID string) ([]UserRoleResponse, error) {
	return api.FetchAllPages[UserRoleResponse](ac, userRolesURL, map[string]string{"role": roleID})
}

func GetRoleHistory(ac *client.AlpaconClient, userID string, limit int) ([]RoleAuditLogResponse, error) {
	params := map[string]string{"user": userID}

	// The newest rows come first, tie-broken by id so a paged walk cannot repeat or skip one,
	// and any ordering parameter would be dropped.
	return api.FetchPagesUpTo[RoleAuditLogResponse](ac, roleAuditLogsURL, params, limit)
}

// The server refuses a second binding of the same role with both scope fields unset, so the
// first workspace-wide match is the only one.
func FindWorkspaceBinding(bindings []UserRoleResponse, roleID string) *UserRoleResponse {
	for i := range bindings {
		if bindings[i].Role.ID == roleID && bindings[i].IsWorkspaceWide() {
			return &bindings[i]
		}
	}

	return nil
}

// Matching by name lets a revoke find the admin companion without resolving a second role.
func FindWorkspaceBindingByName(bindings []UserRoleResponse, roleName string) *UserRoleResponse {
	for i := range bindings {
		if bindings[i].Role.Name == roleName && bindings[i].IsWorkspaceWide() {
			return &bindings[i]
		}
	}

	return nil
}

func HoldsWorkspaceRole(bindings []UserRoleResponse, roleName string) bool {
	return FindWorkspaceBindingByName(bindings, roleName) != nil
}

func IsPlatformTier(roleName string) bool {
	return roleName == RoleAdmin || roleName == RoleSuperuser
}

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
			GrantedAt: localTimestamp(binding.AddedAt),
		})
	}

	return result
}

// A nil usernames map means names were never resolved, so the id—the value other commands
// accept—stands in; the display name is a fallback only when resolution ran and missed.
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
			GrantedAt: localTimestamp(binding.AddedAt),
		})
	}

	return result
}

// Wildcards first: they subsume the concrete rows below them.
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
			Scope:     tierLabel(entry.Scope),
			ChangedBy: changedBy,
			Reason:    entry.Reason,
			At:        localTimestamp(entry.AddedAt),
		})
	}

	return result
}

func GrantRole(ac *client.AlpaconClient, request BindingCreateRequest) error {
	_, err := ac.SendPostRequest(userRolesURL, request)

	return err
}

func RevokeRole(ac *client.AlpaconClient, bindingID, reason string) error {
	url := utils.BuildURL(userRolesURL, bindingID, nil)

	if reason == "" {
		_, err := ac.SendDeleteRequest(url)
		return err
	}

	_, err := ac.SendDeleteRequestWithBody(url, BindingRevokeRequest{Reason: reason})
	return err
}

func IsDuplicateBinding(err error) bool {
	code, _ := utils.ParseErrorResponse(err)
	return code == CodeRoleAssignmentDuplicate
}

// The IAM user endpoint, not /api/rbac/: an API token reaches it where the RBAC routes
// refuse one.
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

// Workspace-wide question only: a permission held through an object-scoped binding
// answers false here, because no object is named.
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
			Scope:  tierLabel(role.Scope),
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

// localTimestamp blanks a missing timestamp: a zero time.Time formats as "0001-01-01 00:00",
// which reads like a real grant date.
func localTimestamp(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.Local().Format(timeLayout)
}

// tierLabel names a scope tier for the sources that carry no id pair and so cannot use
// ScopeLabel. Both words match what ScopeLabel prints for the same tier, so one tier never
// reads as two: "global" becomes "workspace", and "content_type" becomes the "type" that
// heads ScopeLabel's "type:42".
func tierLabel(scope string) string {
	switch scope {
	case "global":
		return "workspace"
	case "content_type":
		return "type"
	default:
		return scope
	}
}
