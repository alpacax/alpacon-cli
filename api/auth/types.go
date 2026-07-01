package auth

import "time"

// PrincipalTypeApplication is the whoami principal_type for service/application tokens.
const PrincipalTypeApplication = "application"

type LoginRequest struct {
	WorkspaceURL  string `json:"workspace_url"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	WorkspaceName string `json:"-"`
	BaseDomain    string `json:"-"`
}

type LoginResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

type APITokenRequest struct {
	Name      string   `json:"name"`
	ExpiresAt *string  `json:"expires_at"`
	Scopes    []string `json:"scopes,omitempty"`
}

type APITokenDuplicateRequest struct {
	Name string `json:"name,omitempty"`
}

type APITokenResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Enabled   bool      `json:"enabled"`
	Key       string    `json:"key"`
	UpdatedAt time.Time `json:"updated_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Scopes    []string  `json:"scopes"`
}

type APITokenAttributes struct {
	ID        string `json:"id" table:"ID"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	UpdatedAt string `json:"updated_at" table:"Updated At"`
	ExpiresAt string `json:"expires_at" table:"Expires At"`
	Scopes    string `json:"scopes" table:"Scopes"`
}

type TokenScopesResponse struct {
	Resources []TokenScopeResource `json:"resources"`
	Wildcards []string             `json:"wildcards"`
}

type TokenScopeResource struct {
	Name    string   `json:"name"`
	Actions []string `json:"actions"`
	ACL     []string `json:"acl"`
}

type TokenScopeAttributes struct {
	Name    string `json:"name" table:"Resource"`
	Actions string `json:"actions" table:"Actions"`
	ACL     string `json:"acl" table:"ACL"`
}

// WhoamiResponse is the GET /api/auth/whoami/ identity. Only the application
// branch is consumed; user principals fall through to iam.GetCurrentUser.
type WhoamiResponse struct {
	PrincipalType string             `json:"principal_type"`
	Auth          WhoamiAuth         `json:"auth"`
	Application   *WhoamiApplication `json:"application,omitempty"`
}

type WhoamiAuth struct {
	Scopes []string `json:"scopes"`
}

type WhoamiApplication struct {
	Name        string `json:"name"`
	ServiceType string `json:"service_type"`
	Username    string `json:"username"` // service account name
}
