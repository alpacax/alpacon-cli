package client

import (
	"os"
	"strings"

	"github.com/alpacax/alpacon-cli/pkg/httpclient"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	// SignerURLEnvVar overrides the signer's base URL. A self-hosted workspace
	// runs its own signer and nothing advertises where it is, so the operator
	// states it here.
	SignerURLEnvVar = "ALPACON_AI_URL"
	// DefaultSignerURL is the signer serving Alpacon Cloud workspaces.
	DefaultSignerURL = "https://alpacon.ai"

	// SignerAudienceEnvVar overrides the Auth0 audience a signer token is
	// requested for, for a self-hosted signer behind its own API identifier.
	SignerAudienceEnvVar = "ALPACON_AI_AUDIENCE"
	// DefaultSignerAudience is the Alpacon Cloud signer's Auth0 audience. An
	// audience is an opaque identifier matched character for character, so its
	// trailing slash is part of it.
	DefaultSignerAudience = "https://alpacon.ai/"
)

// SignerURL returns the signer's base URL, without a trailing slash: the path
// owns its leading one.
func SignerURL() string {
	if raw := strings.TrimSpace(os.Getenv(SignerURLEnvVar)); raw != "" {
		return strings.TrimRight(raw, "/")
	}
	return DefaultSignerURL
}

// SignerAudience returns the Auth0 audience a signer token is requested for,
// verbatim.
func SignerAudience() string {
	if raw := strings.TrimSpace(os.Getenv(SignerAudienceEnvVar)); raw != "" {
		return raw
	}
	return DefaultSignerAudience
}

// NewSignerClient builds a client for the command signer, which holds the
// command pre-approval ledger. The signer is another host behind another Auth0
// audience, so it gets its own client: it carries only the signer token, never
// the workspace login's token or API key, and it never renews a token on a 401,
// since the only renewal this process has mints a token for the workspace.
func NewSignerClient(accessToken string, insecure bool) *AlpaconClient {
	ac := &AlpaconClient{
		HTTPClient: httpclient.New(insecure),
		BaseURL:    SignerURL(),
		UserAgent:  utils.GetUserAgent(),
		Insecure:   insecure,
		noRenewal:  true,
	}
	ac.SetAccessToken(accessToken)
	return ac
}
