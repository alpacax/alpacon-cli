package webhook

import "github.com/alpacax/alpacon-cli/api/types"

type WebhookResponse struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	URL       string            `json:"url"`
	Provider  string            `json:"provider"`
	SSLVerify bool              `json:"ssl_verify"`
	Enabled   bool              `json:"enabled"`
	Owner     types.UserSummary `json:"owner"`
	AddedAt   string            `json:"added_at"`
	UpdatedAt string            `json:"updated_at"`
}

type WebhookAttributes struct {
	ID        string `json:"id" table:"ID"`
	Name      string `json:"name" table:"Name"`
	URL       string `json:"url" table:"URL"`
	Provider  string `json:"provider" table:"Provider"`
	SSLVerify bool   `json:"ssl_verify" table:"SSL Verify"`
	Enabled   bool   `json:"enabled" table:"Enabled"`
	Owner     string `json:"owner"`
}

type WebhookCreateRequest struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Provider  string `json:"provider,omitempty"`
	SSLVerify bool   `json:"ssl_verify"`
	Enabled   bool   `json:"enabled"`
	Owner     string `json:"owner"`
}
