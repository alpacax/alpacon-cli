package config

// Config describes the configuration for Alpacon CLI
type Config struct {
	WorkspaceURL string `json:"workspace_url"`
	// WorkspaceName is the label of the workspace URL's host, as typed at login.
	// A workspace's URL slug can be renamed, so it is not the workspace's
	// identity; see SchemaName and WorkspaceIdentity.
	WorkspaceName string `json:"workspace_name"`
	// SchemaName is the workspace's frozen identity, taken from the server's
	// auth environment at login. Empty in a config written before the field
	// existed, or by a server that does not report it.
	SchemaName           string `json:"schema_name,omitempty"`
	Token                string `json:"token,omitempty"`
	ExpiresAt            string `json:"expires_at,omitempty"`
	AccessToken          string `json:"access_token,omitempty"`
	RefreshToken         string `json:"refresh_token,omitempty"`
	AccessTokenExpiresAt string `json:"access_token_expires_at,omitempty"`
	BaseDomain           string `json:"base_domain,omitempty"`
	Insecure             bool   `json:"insecure"`
	// ActiveWorkSessions maps workspace identity (see WorkspaceIdentity) to active work-session UUID.
	// Nil (not an empty map) when the key is absent from the JSON config file.
	ActiveWorkSessions map[string]string `json:"active_work_sessions,omitempty"`
}

// WorkspaceIdentity is the value that names the current workspace to the server
// and in the JWT workspaces claim: the schema name, or the host label for a
// config that predates it. The next login records the schema name.
func (c Config) WorkspaceIdentity() string {
	if c.SchemaName != "" {
		return c.SchemaName
	}
	return c.WorkspaceName
}

// IsMultiWorkspaceMode returns true if the user logged in via Auth0 with a known base domain,
// enabling workspace listing and switching from the JWT.
func (c Config) IsMultiWorkspaceMode() bool {
	return c.AccessToken != "" && c.BaseDomain != ""
}
