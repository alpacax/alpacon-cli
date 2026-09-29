// Package httpclient builds the HTTP client every request carrying a
// credential goes through: the workspace API, login, logout and the Auth0
// token exchanges.
//
// Go's default redirect policy re-attaches Authorization whenever the
// destination hostname matches, whatever the scheme or port, and replays a
// request body on a 307 or 308 to any host. This client follows a redirect
// only while it stays on the origin of the request that started the chain.
//
// It lives outside package client because api/auth0 needs it and package
// client imports api/auth0.
package httpclient

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

const (
	// maxRedirects is the cap Go's default redirect policy applies.
	maxRedirects = 10

	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

// New returns a client that follows the system proxy settings as Go's default
// transport does, requires TLS 1.2, skips certificate verification only when
// insecure is set, and refuses any redirect that leaves the origin.
func New(insecure bool) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{
				MinVersion:         tls.VersionTLS12,
				InsecureSkipVerify: insecure, //nolint:gosec
			},
		},
		CheckRedirect: sameOriginRedirect,
	}
}

// StopAtCrossOrigin returns a copy of c that hands back a redirect leaving the
// origin as the 3xx response instead of failing, so the caller can show it.
func StopAtCrossOrigin(c *http.Client) *http.Client {
	stopped := *c
	stopped.CheckRedirect = stopAtCrossOrigin
	return &stopped
}

// sameOriginRedirect is an http.Client CheckRedirect policy. It compares each
// hop with via[0], the request that started the chain, so a chain cannot walk
// off the origin one allowed step at a time.
func sameOriginRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	from, to := via[0].URL, req.URL
	if !redirectAllowed(from, to) {
		return fmt.Errorf("refusing redirect from %s to %s", displayURL(from), displayURL(to))
	}
	return nil
}

// stopAtCrossOrigin is sameOriginRedirect, but ends the chain with the 3xx
// instead of an error.
func stopAtCrossOrigin(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	if !redirectAllowed(via[0].URL, req.URL) {
		return http.ErrUseLastResponse
	}
	return nil
}

// redirectAllowed reports whether to stays on from's origin. The only scheme
// change it accepts is http to https on the same hostname, landing on the
// default https port or on the port the original request named.
func redirectAllowed(from, to *url.URL) bool {
	fromScheme, toScheme := strings.ToLower(from.Scheme), strings.ToLower(to.Scheme)
	if !strings.EqualFold(from.Hostname(), to.Hostname()) {
		return false
	}
	switch {
	case fromScheme == toScheme && (fromScheme == schemeHTTP || fromScheme == schemeHTTPS):
		return effectivePort(from) == effectivePort(to)
	case fromScheme == schemeHTTP && toScheme == schemeHTTPS:
		return effectivePort(to) == "443" || (from.Port() != "" && to.Port() == from.Port())
	default:
		return false
	}
}

// effectivePort is the port a request to u dials, so an explicit default port
// compares equal to none.
func effectivePort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	switch strings.ToLower(u.Scheme) {
	case schemeHTTP:
		return "80"
	case schemeHTTPS:
		return "443"
	}
	return ""
}

// displayURL drops userinfo, query and fragment, the parts of a URL that can
// carry a credential such as a presigned signature.
func displayURL(u *url.URL) string {
	shown := url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path, RawPath: u.RawPath}
	return shown.String()
}
