package httpclient

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// request builds a hop the way net/http hands it to CheckRedirect: only its
// URL is read.
func request(t *testing.T, raw string) *http.Request {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return &http.Request{Method: http.MethodGet, URL: u}
}

func TestRedirectAllowed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		from  string
		to    string
		allow bool
	}{
		{name: "same origin", from: "https://ws.example.com/a", to: "https://ws.example.com/a/", allow: true},
		{name: "hostname differs only in case", from: "https://ws.example.com/a", to: "https://WS.Example.COM/b", allow: true},
		{name: "explicit default https port", from: "https://ws.example.com/a", to: "https://ws.example.com:443/b", allow: true},
		{name: "explicit default http port", from: "http://ws.example.com:80/a", to: "http://ws.example.com/b", allow: true},
		{name: "same explicit port", from: "https://ws.example.com:8443/a", to: "https://ws.example.com:8443/b", allow: true},
		{name: "IPv6 same origin", from: "https://[::1]:8443/a", to: "https://[::1]:8443/b", allow: true},
		{name: "http upgrades to https on the default port", from: "http://ws.example.com/a", to: "https://ws.example.com/a", allow: true},
		{name: "http on a custom port upgrades to https on the default port", from: "http://ws.example.com:8080/a", to: "https://ws.example.com/a", allow: true},
		{name: "http upgrades to https on the same explicit port", from: "http://ws.example.com:8080/a", to: "https://ws.example.com:8080/a", allow: true},
		{name: "IPv6 http upgrades to https", from: "http://[::1]/a", to: "https://[::1]/a", allow: true},

		{name: "https downgrades to http", from: "https://ws.example.com/a", to: "http://ws.example.com/a"},
		{name: "https downgrades to http on the same explicit port", from: "https://ws.example.com:8443/a", to: "http://ws.example.com:8443/a"},
		{name: "http upgrades to https on another port", from: "http://ws.example.com/a", to: "https://ws.example.com:8443/a"},
		{name: "same scheme on another port", from: "https://ws.example.com/a", to: "https://ws.example.com:8443/a"},
		{name: "explicit port dropped", from: "https://ws.example.com:8443/a", to: "https://ws.example.com/a"},
		{name: "another host", from: "https://ws.example.com/a", to: "https://other.example.com/a"},
		{name: "subdomain", from: "https://example.com/a", to: "https://ws.example.com/a"},
		{name: "IPv6 another port", from: "https://[::1]:8443/a", to: "https://[::1]:9443/a"},
		{name: "IPv4 to IPv6 loopback", from: "https://127.0.0.1/a", to: "https://[::1]/a"},
		{name: "non-http scheme", from: "https://ws.example.com/a", to: "ftp://ws.example.com/a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			from := request(t, tt.from)
			to := request(t, tt.to)

			err := sameOriginRedirect(to, []*http.Request{from})

			if tt.allow {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, "refusing redirect from "+tt.from+" to "+tt.to)
			}
		})
	}
}

// Every hop is judged against the request that started the chain, so a
// chain cannot step off the origin through an allowed intermediate hop.
func TestSameOriginRedirect_ComparesWithTheFirstRequest(t *testing.T) {
	t.Parallel()
	first := request(t, "https://ws.example.com/a")
	second := request(t, "https://ws.example.com/b")
	next := request(t, "https://ws.example.com:8443/c")

	err := sameOriginRedirect(next, []*http.Request{first, second})

	assert.EqualError(t, err, "refusing redirect from https://ws.example.com/a to https://ws.example.com:8443/c")
}

func TestSameOriginRedirect_OmitsUserinfoQueryAndFragment(t *testing.T) {
	t.Parallel()
	from := request(t, "https://ws.example.com/a?X-Amz-Signature=first")
	to := request(t, "http://user:secret@ws.example.com/b?X-Amz-Signature=second#frag")

	err := sameOriginRedirect(to, []*http.Request{from})

	assert.EqualError(t, err, "refusing redirect from https://ws.example.com/a to http://ws.example.com/b")
}

func TestSameOriginRedirect_StopsAfterTenRedirects(t *testing.T) {
	t.Parallel()
	req := request(t, "https://ws.example.com/a")
	via := make([]*http.Request, 0, maxRedirects)
	for range maxRedirects - 1 {
		via = append(via, req)
	}

	require.NoError(t, sameOriginRedirect(req, via))
	assert.EqualError(t, sameOriginRedirect(req, append(via, req)), "stopped after 10 redirects")
}

func TestNew(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		insecure bool
	}{
		{name: "verifying", insecure: false},
		{name: "insecure", insecure: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := New(tt.insecure)

			transport, ok := c.Transport.(*http.Transport)
			require.True(t, ok, "transport is %T", c.Transport)
			assert.Equal(t, uint16(tls.VersionTLS12), transport.TLSClientConfig.MinVersion)
			assert.Equal(t, tt.insecure, transport.TLSClientConfig.InsecureSkipVerify)
			// Compared by pointer: ProxyFromEnvironment reads the environment
			// once per process and never proxies a loopback host.
			require.NotNil(t, transport.Proxy)
			assert.Equal(t, reflect.ValueOf(http.ProxyFromEnvironment).Pointer(), reflect.ValueOf(transport.Proxy).Pointer())
			assert.NotNil(t, c.CheckRedirect)
		})
	}
}
