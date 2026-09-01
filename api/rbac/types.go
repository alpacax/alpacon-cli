package rbac

import (
	"strconv"
	"time"

	"github.com/alpacax/alpacon-cli/api/types"
)

type RoleResponse struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	AddedAt     time.Time `json:"added_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type RoleNested struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ContentType and ObjectID are the scope pair; only a workspace-wide binding makes
// someone an admin or a superuser.
type UserRoleResponse struct {
	ID          string            `json:"id"`
	User        types.UserSummary `json:"user"`
	Role        RoleNested        `json:"role"`
	ContentType *int              `json:"content_type"`
	ObjectID    *string           `json:"object_id"`
	AddedAt     time.Time         `json:"added_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type RoleScopesResponse struct {
	Resources []RoleScopeResource `json:"resources"`
	Wildcards []string            `json:"wildcards"`
}

type RoleScopeResource struct {
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
	ACL     []string `json:"acl"`
}

// The only surface carrying the justification and the actor; a binding read carries
// neither. Role, Principal and ChangedBy answer null where there is nothing left to
// name - the row keeps value snapshots that survive deletion of what they point at.
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

// User and Role are scalars on purpose: more than one value in any of the three fields
// flips the server onto a bulk path that answers 201 with an empty body whether or not
// it created anything, and never reports a duplicate.
// ContentType and ObjectID are unset by every command so far, kept for later
// object-scoped writes.
type BindingCreateRequest struct {
	User        string   `json:"user"`
	Role        string   `json:"role"`
	ContentType *int     `json:"content_type,omitempty"`
	ObjectID    []string `json:"object_id,omitempty"`
	Reason      string   `json:"reason,omitempty"`
}

// The justification rides the DELETE body, not a query parameter, so it stays out of
// access logs, proxy logs and shell history.
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

// Roles lists global and content-type-wide bindings only: an owner role is one row per
// object, so including object-scoped ones would make this an unbounded payload.
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

// ObjectScoped patterns are reachable only through a narrower binding: the user holds
// them somewhere, but the endpoint does not say on what.
type PermissionPatternsResponse struct {
	Global       []string `json:"global"`
	ObjectScoped []string `json:"object_scoped"`
}

// What GET /api/iam/users/{id}/permissions/ returns instead when a permission query
// parameter is sent.
type PermissionCheckResponse struct {
	Allowed bool `json:"allowed"`
}

// POST /api/rbac/troubleshoot/ reads rather than writes, despite the verb.
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

// The scope columns are nullable and blankable, so an empty ObjectID counts as unscoped too.
func (b UserRoleResponse) IsWorkspaceWide() bool {
	return b.ContentType == nil && (b.ObjectID == nil || *b.ObjectID == "")
}

// Content types print as their numeric id: the CLI grants nothing object-scoped yet, so
// resolving names via /api/rbac/content-types/ would not earn a request.
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
