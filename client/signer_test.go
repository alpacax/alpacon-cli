package client

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignerURL(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want string
	}{
		{name: "default", env: "", want: DefaultSignerURL},
		{name: "override", env: "https://signer.example.com", want: "https://signer.example.com"},
		{name: "override with trailing slash", env: "https://signer.example.com/", want: "https://signer.example.com"},
		{name: "blank override", env: "   ", want: DefaultSignerURL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(SignerURLEnvVar, tt.env)
			assert.Equal(t, tt.want, SignerURL())
		})
	}
}

// An audience is matched character for character, so its trailing slash is
// part of it and must survive both the default and an override.
func TestSignerAudience_KeepsTheTrailingSlash(t *testing.T) {
	t.Setenv(SignerAudienceEnvVar, "")
	assert.Equal(t, "https://alpacon.ai/", SignerAudience())

	t.Setenv(SignerAudienceEnvVar, "https://signer.example.com/")
	assert.Equal(t, "https://signer.example.com/", SignerAudience())
}

func TestNewSignerClient_SendsOnlyTheSignerToken(t *testing.T) {
	t.Setenv(SignerURLEnvVar, "")
	ac := NewSignerClient("signer-token")

	assert.Equal(t, DefaultSignerURL, ac.BaseURL)

	req, err := ac.createRequest(http.MethodGet, "/api/acl/me/", nil)
	require.NoError(t, err)
	assert.Equal(t, "Bearer signer-token", req.Header.Get("Authorization"))
}

// The refresh-token grant mints a token for the workspace audience. Running it
// on a signer 401 would replace the signer token with one the signer refuses,
// so the 401 has to surface as it is.
func TestSignerClient_NeverRenewsOnAStaleToken(t *testing.T) {
	renewals := stubTokenRenewal(t, "workspace-token")

	requests := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		staleTokenHandler(w)
	}))
	defer ts.Close()

	t.Setenv(SignerURLEnvVar, ts.URL)
	ac := NewSignerClient("signer-token")
	_, err := ac.SendGetRequest("/api/acl/me/")

	require.Error(t, err)
	assert.Equal(t, 0, *renewals)
	assert.Equal(t, 1, requests)
	assert.Equal(t, "signer-token", ac.AccessToken())
}

func TestNewSignerClient_VerifiesTheCertificate(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()
	t.Setenv(SignerURLEnvVar, ts.URL)

	_, err := NewSignerClient("signer-token").SendGetRequest("/api/acl/me/")

	require.Error(t, err, "a certificate no system root signed must be refused")
	assert.False(t, NewSignerClient("signer-token").Insecure)
}

func TestCheckSignerURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{name: "https", raw: "https://alpacon.ai"},
		{name: "loopback http", raw: "http://127.0.0.1:8400"},
		{name: "localhost http", raw: "http://localhost:8400"},
		{name: "remote http", raw: "http://signer.example.com", wantErr: true},
		{name: "other scheme", raw: "ftp://signer.example.com", wantErr: true},
		{name: "no host", raw: "signer.example.com", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := CheckSignerURL(tt.raw)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
