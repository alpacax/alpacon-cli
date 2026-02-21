package webhook

import "github.com/alpacax/alpacon-cli/api/types"

type WebhookResponse struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	URL       string            `json:"url"`
	SSLVerify bool              `json:"ssl_verify"`
	Enabled   bool              `json:"enabled"`
	Owner     types.UserSummary `json:"owner"`
}

type WebhookAttributes struct {
	ID        string `json:"id" table:"ID"`
	Name      string `json:"name"`
	URL       string `json:"url" table:"URL"`
	SSLVerify bool   `json:"ssl_verify" table:"SSL Verify"`
	Enabled   bool   `json:"enabled"`
	Owner     string `json:"owner"`
}

type WebhookCreateRequest struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	SSLVerify bool   `json:"ssl_verify"`
	Enabled   bool   `json:"enabled"`
	Owner     string `json:"owner"`
}
