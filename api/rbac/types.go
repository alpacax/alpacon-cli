package rbac

import (
	"strconv"
	"time"

	"github.com/alpacax/alpacon-cli/api/types"
)

// RoleResponse is one row of GET /api/rbac/roles/.
type RoleResponse struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	AddedAt     time.Time `json:"added_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// RoleNested is the {id, name} object the binding and audit serializers embed in
// place of the role's primary key. The binding serializer overwrites its own
// PrimaryKeyRelatedField with this in to_representation, so a read never carries
// a bare role UUID.
type RoleNested struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// UserRoleResponse is one role binding. ContentType and ObjectID are the scope
// pair: both empty means the binding is workspace-wide, and only a workspace-wide
// binding decides whether someone is an admin or a superuser.
type UserRoleResponse struct {
	ID          string            `json:"id"`
	User        types.UserSummary `json:"user"`
	Role        RoleNested        `json:"role"`
	ContentType *int              `json:"content_type"`
	ObjectID    *string           `json:"object_id"`
	AddedAt     time.Time         `json:"added_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// RoleScopesResponse is GET /api/rbac/roles/{id}/scopes/. Concrete resource:action
// permissions are grouped by resource; wildcards are listed apart because they
// stand for whole tiers of access rather than a named action.
type RoleScopesResponse struct {
	Resources []RoleScopeResource `json:"resources"`
	Wildcards []string            `json:"wildcards"`
}

type RoleScopeResource struct {
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
	ACL     []string `json:"acl"`
}

// RoleAuditLogResponse is one row of GET /api/rbac/role-audit-logs/. It is where
// the justification and the actor live: a binding read carries neither.
//
// Role, Principal and ChangedBy are pointers because the server renders them from
// value snapshots that survive deletion of what they point at, and answers null
// where there is nothing to name.
type RoleAuditLogResponse struct {
	ID          string          `json:"id"`
	RecordClass string          `json:"record_class"`
	Action      string          `json:"action"`
	Role        *RoleNested     `json:"role"`
	RoleName    string          `json:"role_name"`
	Principal   *AuditPrincipal `json:"principal"`
	Scope       string          `json:"scope"`
	ChangedBy   *AuditActor     `json:"changed_by"`
	Reason      string          `json:"reason"`
	AddedAt     time.Time       `json:"added_at"`
}

type AuditPrincipal struct {
	Type  *string `json:"type"`
	ID    string  `json:"id"`
	Label string  `json:"label"`
}

type AuditActor struct {
	ID    *string `json:"id"`
	Label string  `json:"label"`
}

// BindingCreateRequest is the POST /api/rbac/user-roles/ body. User and Role are
// scalars on purpose: the server switches to a bulk path as soon as any of the
// three fields carries more than one value, and that path answers 201 with an
// empty body whether or not it created anything. Only the scalar path reports a
// duplicate, which is what lets a grant converge instead of guessing.
//
// ContentType and ObjectID stay on the struct though no command sets them yet, so
// object-scoped writes can be added later without touching this signature.
type BindingCreateRequest struct {
	User        string   `json:"user"`
	Role        string   `json:"role"`
	ContentType *int     `json:"content_type,omitempty"`
	ObjectID    []string `json:"object_id,omitempty"`
	Reason      string   `json:"reason,omitempty"`
}

// BindingRevokeRequest carries the justification on the DELETE body rather than in
// a query parameter, so it stays out of access logs, proxy logs and shell history.
type BindingRevokeRequest struct {
	Reason string `json:"reason"`
}

type RoleAttributes struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type UserRoleAttributes struct {
	Role      string `json:"role"`
	Scope     string `json:"scope"`
	GrantedAt string `json:"granted_at" table:"Granted At"`
}

type RoleHolderAttributes struct {
	User      string `json:"user"`
	Scope     string `json:"scope"`
	GrantedAt string `json:"granted_at" table:"Granted At"`
}

type RoleScopeAttributes struct {
	Name    string `json:"name" table:"Resource"`
	Actions string `json:"actions"`
	ACL     string `json:"acl" table:"ACL"`
}

type RoleAuditAttributes struct {
	Action    string `json:"action"`
	Role      string `json:"role"`
	Scope     string `json:"scope"`
	ChangedBy string `json:"changed_by" table:"Changed By"`
	Reason    string `json:"reason"`
	At        string `json:"at"`
}

// EffectivePermissionsResponse is GET /api/iam/users/{id}/effective-permissions/:
// where a user's authority comes from, and what it adds up to.
//
// Roles lists global and content-type-wide bindings only. Object-scoped ones are
// left out on purpose—an owner role is one row per object, so an account that owns
// many servers would make this an unbounded payload.
type EffectivePermissionsResponse struct {
	User        types.UserSummary  `json:"user"`
	Summary     EffectiveSummary   `json:"summary"`
	Roles       []EffectiveRole    `json:"roles"`
	Permissions RoleScopesResponse `json:"permissions"`
}

type EffectiveSummary struct {
	PermissionCount int `json:"permission_count"`
	RoleCount       int `json:"role_count"`
	GroupCount      int `json:"group_count"`
}

type EffectiveRole struct {
	Role RoleNested `json:"role"`
	// Source is "user" for a role bound directly, "group" for one inherited.
	Source string       `json:"source"`
	Group  *GroupNested `json:"group"`
	// Scope is "global" or "content_type"; see EffectivePermissionsResponse.
	Scope string `json:"scope"`
}

type GroupNested struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

// PermissionPatternsResponse is GET /api/iam/users/{id}/permissions/. Global holds
// the patterns granted workspace-wide; ObjectScoped holds patterns reachable only
// through a narrower binding, which says the user has them somewhere without saying
// on what.
type PermissionPatternsResponse struct {
	Global       []string `json:"global"`
	ObjectScoped []string `json:"object_scoped"`
}

// PermissionCheckResponse is the check-mode answer of the same endpoint.
type PermissionCheckResponse struct {
	Allowed bool `json:"allowed"`
}

// TroubleshootRequest is the POST /api/rbac/troubleshoot/ body. The endpoint reads
// rather than writes despite the verb.
type TroubleshootRequest struct {
	UserID     string `json:"user_id"`
	Permission string `json:"permission"`
}

type EffectiveRoleAttributes struct {
	Role   string `json:"role"`
	Source string `json:"source"`
	Group  string `json:"group"`
	Scope  string `json:"scope"`
}

type PermissionPatternAttributes struct {
	Permission string `json:"permission"`
	Scope      string `json:"scope"`
}

// IsWorkspaceWide reports whether the binding carries no object scope. The scope
// columns are nullable and blankable, so an empty ObjectID counts as unscoped too.
func (b UserRoleResponse) IsWorkspaceWide() bool {
	return b.ContentType == nil && (b.ObjectID == nil || *b.ObjectID == "")
}

// ScopeLabel renders the binding's scope tier for display. Content types are shown
// by their numeric id because the CLI grants nothing object-scoped yet and so has
// no reason to spend a request on /api/rbac/content-types/ resolving names.
func (b UserRoleResponse) ScopeLabel() string {
	if b.IsWorkspaceWide() {
		return "workspace"
	}

	contentType := "?"
	if b.ContentType != nil {
		contentType = strconv.Itoa(*b.ContentType)
	}
	if b.ObjectID == nil || *b.ObjectID == "" {
		return "type:" + contentType
	}
	return "type:" + contentType + "/" + *b.ObjectID
}
