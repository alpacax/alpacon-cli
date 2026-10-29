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
	// SignerURLEnvVar overrides the signer's base URL; nothing advertises where
	// a self-hosted signer is.
	SignerURLEnvVar = "ALPACON_AI_URL"
	// DefaultSignerURL is the signer serving Alpacon Cloud workspaces.
	DefaultSignerURL = "https://alpacon.ai"

	// SignerAudienceEnvVar overrides the Auth0 audience a signer token is
	// requested for, for a self-hosted signer behind its own API identifier.
	SignerAudienceEnvVar = "ALPACON_AI_AUDIENCE"
	// DefaultSignerAudience is the Alpacon Cloud signer's Auth0 audience, matched
	// character for character, trailing slash included.
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

// CheckSignerURL refuses a signer URL the token must not be sent to: anything
// but https, except plain http to a loopback host.
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

// NewSignerClient builds a signer client carrying only the signer token. It never
// renews on a 401 and always verifies the certificate, whatever the workspace chose.
func NewSignerClient(accessToken string) *AlpaconClient {
	ac := &AlpaconClient{
		HTTPClient:       httpclient.New(false),
		BaseURL:          SignerURL(),
		UserAgent:        utils.GetUserAgent(),
		noRenewal:        true,
		dropsRefusalNext: true,
	}
	ac.SetAccessToken(accessToken)
	return ac
}
