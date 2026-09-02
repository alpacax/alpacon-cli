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

// Only a workspace-wide binding makes someone an admin or a superuser.
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

// The only surface carrying the justification and the actor—a binding read carries
// neither. Role, Principal and ChangedBy answer null where there is nothing left to
// name, so the row keeps value snapshots like RoleName.
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
// flips the server onto a bulk path that answers 201 with an empty body even when it
// created nothing, and never reports a duplicate.
// ContentType and ObjectID are unset for now, kept for later object-scoped writes.
type BindingCreateRequest struct {
	User        string   `json:"user"`
	Role        string   `json:"role"`
	ContentType *int     `json:"content_type,omitempty"`
	ObjectID    []string `json:"object_id,omitempty"`
	Reason      string   `json:"reason,omitempty"`
}

// The justification rides the DELETE body, not a query parameter, so it stays out of
// access and proxy logs.
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

// Roles omits object-scoped bindings: an owner role is one row per object, so the payload
// would be unbounded.
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

// ObjectScoped says the user holds the pattern somewhere, never on what object.
type PermissionPatternsResponse struct {
	Global       []string `json:"global"`
	ObjectScoped []string `json:"object_scoped"`
}

// GET /api/iam/users/{id}/permissions/ answers this when a permission query parameter is sent.
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

// An unscoped binding answers null or an empty ObjectID, so a nil check alone would miss one.
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
