package client

import (
	"fmt"
	"net"
	"net/url"
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

// CheckSignerURL refuses a signer URL the signer token must not be sent to:
// anything but https, except plain http to a loopback host for a signer run on
// the same machine.
func CheckSignerURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("%s is not a URL", SignerURLEnvVar)
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if parsed.Scheme == "http" && isLoopback(parsed.Hostname()) {
		return nil
	}
	return fmt.Errorf("%s must be an https URL; the signer token is not sent over %s", SignerURLEnvVar, parsed.Scheme)
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
// since the only renewal this process has mints a token for the workspace. It
// always verifies the signer's certificate: a workspace logged in with
// --insecure opted out for that workspace's host, not for the signer's.
func NewSignerClient(accessToken string) *AlpaconClient {
	ac := &AlpaconClient{
		HTTPClient: httpclient.New(false),
		BaseURL:    SignerURL(),
		UserAgent:  utils.GetUserAgent(),
		noRenewal:  true,
	}
	ac.SetAccessToken(accessToken)
	return ac
}
