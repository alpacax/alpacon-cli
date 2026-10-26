package auth0

import "encoding/json"

type DeviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
	VerificationURIComplete string `json:"verification_uri_complete"`
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
	Scope        string `json:"scope"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Error        string `json:"error,omitempty"`
	ErrorDesc    string `json:"error_description,omitempty"`
}

type Auth0Config struct {
	Method   string `json:"method"`
	ClientID string `json:"client_id,omitempty"`
	Domain   string `json:"domain,omitempty"`
	Audience string `json:"audience,omitempty"`
	// SchemaName is the frozen workspace identity, which equals the Auth0
	// organization name. Prefer it over the workspace URL label when building
	// the org scope—the URL label can change while this never does. Empty on
	// older servers that do not return the field.
	SchemaName string `json:"schema_name,omitempty"`
	// WorkspaceID is the workspace's UUID, which the signer's ledger names a
	// workspace by. Empty on older servers that do not return the field.
	WorkspaceID string `json:"workspace_id,omitempty"`
}

type AuthEnvResponse struct {
	Auth0    Auth0Config `json:"auth0"`
	Language string      `json:"language"`
	Surfaces Surfaces    `json:"surfaces"`
	// CommandACLSurface is CommandACLSurfaceAI when the workspace's command ACLs
	// live on the command pre-approval ledger.
	CommandACLSurface CommandACLSurface `json:"command_acl_surface"`
}

// CommandACLSurfaceAI is the command_acl_surface value that sends command ACL
// management to the command pre-approval ledger.
const CommandACLSurfaceAI = "ai"

// CommandACLSurface is the server's command_acl_surface answer, or "" when the
// server predates the key.
type CommandACLSurface string

// UnmarshalJSON never fails, for the reason Surfaces gives: anything that is
// not a JSON string reads as "".
func (s *CommandACLSurface) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		value = ""
	}
	*s = CommandACLSurface(value)
	return nil
}

// Surfaces lists the optional product surfaces the server says this workspace
// exposes. A server that predates the key leaves every surface false.
type Surfaces struct {
	Kubernetes bool
}

// UnmarshalJSON never fails. The same response feeds login, token refresh,
// revoke and logout, so a malformed surfaces value must not take those down;
// anything that is not a JSON object carrying a boolean reads as false.
func (s *Surfaces) UnmarshalJSON(data []byte) error {
	*s = Surfaces{}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	s.Kubernetes, _ = raw["kubernetes"].(bool)
	return nil
}
