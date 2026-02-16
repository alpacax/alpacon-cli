package auth

import "time"

type LoginRequest struct {
	WorkspaceURL string `json:"workspace_url"`
	Username     string `json:"username"`
	Password     string `json:"password"`
}

type LoginResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

type APITokenRequest struct {
	Name      string  `json:"name"`
	ExpiresAt *string `json:"expires_at"`
}

type APITokenResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Enabled   bool      `json:"enabled"`
	Key       string    `json:"key"`
	UpdatedAt time.Time `json:"updated_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type APITokenAttributes struct {
	ID        string `json:"id" table:"ID"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled"`
	UpdatedAt string `json:"updated_at" table:"Updated At"`
	ExpiresAt string `json:"expires_at" table:"Expires At"`
}
