package config

// Config describes the configuration for Alpacon CLI
type Config struct {
	WorkspaceURL         string `json:"workspace_url"`
	WorkspaceName        string `json:"workspace_name"`
	Token                string `json:"token,omitempty"`
	ExpiresAt            string `json:"expires_at,omitempty"`
	AccessToken          string `json:"access_token,omitempty"`
	RefreshToken         string `json:"refresh_token,omitempty"`
	AccessTokenExpiresAt string `json:"access_token_expires_at,omitempty"`
	BaseDomain           string `json:"base_domain,omitempty"`
	Insecure             bool   `json:"insecure"`
}

// IsMultiWorkspaceMode returns true if the user logged in via Auth0 with a known base domain,
// enabling workspace listing and switching from the JWT.
func (c Config) IsMultiWorkspaceMode() bool {
	return c.AccessToken != "" && c.BaseDomain != ""
}
