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
	Insecure             bool   `json:"insecure"`
}
