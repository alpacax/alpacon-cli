package client

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type rawRoundTripFunc func(*http.Request) (*http.Response, error)

func (f rawRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func rawTestResponse(status int, contentType, body string) *http.Response {
	return &http.Response{Status: http.StatusText(status), StatusCode: status, Proto: "HTTP/1.1", Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(bytes.NewBufferString(body))}
}

func TestSendRawRequest_PreservesResponse(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status int
		ct     string
		body   string
	}{
		{"json success", http.StatusOK, "application/json", `{"ok":true}`},
		{"json refusal", http.StatusNotFound, "application/json", `{"detail":"missing"}`},
		{"html error", http.StatusInternalServerError, "text/html", `<h1>error</h1>`},
		{"csv success", http.StatusOK, "text/csv", "a,b\n1,2\n"},
		{"empty success", http.StatusNoContent, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ac := newTestClient("https://workspace.example")
			ac.HTTPClient.Transport = rawRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				return rawTestResponse(tc.status, tc.ct, tc.body), nil
			})

			resp, err := ac.SendRawRequest(http.MethodGet, "/api/test", nil, nil)

			require.NoError(t, err)
			assert.Equal(t, tc.status, resp.StatusCode)
			assert.Equal(t, tc.ct, resp.Header.Get("Content-Type"))
			assert.Equal(t, tc.body, string(resp.Body))
		})
	}
}

func TestSendRawRequest_RenewsAndReplaysBody(t *testing.T) {
	renewals := stubTokenRenewal(t, "fresh")
	var bodies, tokens []string
	ac := newBearerTestClient("https://workspace.example", "stale")
	ac.HTTPClient.Transport = rawRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		tokens = append(tokens, r.Header.Get("Authorization"))
		if len(bodies) == 1 {
			return rawTestResponse(http.StatusUnauthorized, "application/json", `{"detail":"expired"}`), nil
		}
		return rawTestResponse(http.StatusCreated, "application/json", `{"ok":true}`), nil
	})

	resp, err := ac.SendRawRequest(http.MethodPost, "/api/test", bytes.NewReader([]byte(`{"a":1}`)), nil)

	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, 1, *renewals)
	assert.Equal(t, []string{`{"a":1}`, `{"a":1}`}, bodies)
	assert.Equal(t, []string{"Bearer stale", "Bearer fresh"}, tokens)
}

func TestSendRawRequest_RejectsContentLengthForUnknownBodySize(t *testing.T) {
	t.Parallel()
	requests := 0
	ac := newTestClient("https://workspace.example")
	ac.HTTPClient.Transport = rawRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return rawTestResponse(http.StatusNoContent, "", ""), nil
	})
	header := make(http.Header)
	header.Set("Content-Length", "0")

	_, err := ac.SendRawRequest(http.MethodPost, "/x", io.NopCloser(strings.NewReader("x")), header)

	require.Error(t, err)
	assert.Equal(t, 0, requests)
}

func TestSendRawRequest_CodedUnauthorizedDoesNotRenew(t *testing.T) {
	renewals := stubTokenRenewal(t, "fresh")
	requests := 0
	ac := newBearerTestClient("https://workspace.example", "stale")
	ac.HTTPClient.Transport = rawRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return rawTestResponse(http.StatusUnauthorized, "application/json", `{"code":"auth_mfa_required"}`), nil
	})

	resp, err := ac.SendRawRequest(http.MethodGet, "/api/test", nil, nil)

	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.JSONEq(t, `{"code":"auth_mfa_required"}`, string(resp.Body))
	assert.Equal(t, 0, *renewals)
	assert.Equal(t, 1, requests)
}

func TestSendRawRequest_DoesNotFollowRedirectOffWorkspaceHost(t *testing.T) {
	t.Parallel()
	externalRequests := 0
	ac := newBearerTestClient("https://workspace.example", "secret")
	ac.HTTPClient.Transport = rawRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "evil.example" {
			externalRequests++
			return rawTestResponse(http.StatusOK, "text/plain", "external"), nil
		}
		response := rawTestResponse(http.StatusFound, "text/plain", "redirect")
		response.Header.Set("Location", "https://evil.example/x")
		return response, nil
	})

	response, err := ac.SendRawRequest(http.MethodGet, "/x", nil, nil)

	require.NoError(t, err)
	assert.Equal(t, http.StatusFound, response.StatusCode)
	assert.Equal(t, 0, externalRequests)
}

func TestNormalizeRawEndpoint_RestrictsToWorkspacePath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		path string
		want string
		bad  bool
	}{
		{"relative", "api/v1/x", "/api/v1/x", false},
		{"absolute path", "/api/v1/x?foo=1", "/api/v1/x?foo=1", false},
		{"url", "https://evil.example/x", "", true},
		{"network path", "//evil.example/x", "", true},
		{"scheme path", "http:/x", "", true},
		{"query value with scheme is not a scheme", "/api/hooks?callback=https://example.com/result", "/api/hooks?callback=https://example.com/result", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeRawEndpoint(tc.path)

			if tc.bad {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSendRawRequest_AcceptsCallbackQueryWithSchemeAndKeepsWorkspaceHost(t *testing.T) {
	t.Parallel()
	var gotHost, gotQuery string
	ac := newTestClient("https://workspace.example")
	ac.HTTPClient.Transport = rawRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotHost = r.URL.Host
		gotQuery = r.URL.RawQuery
		return rawTestResponse(http.StatusOK, "application/json", `{}`), nil
	})

	_, err := ac.SendRawRequest(http.MethodGet, "/api/hooks?callback=https://example.com/result", nil, nil)

	require.NoError(t, err)
	assert.Equal(t, "workspace.example", gotHost)
	assert.Equal(t, "callback=https://example.com/result", gotQuery)
}

func TestSendRawRequest_EncodedOrBackslashSlashesDoNotChangeRequestHost(t *testing.T) {
	t.Parallel()
	for _, endpoint := range []string{"/api/x%2F%2Fevil", `/api/x\\evil`, `/api\evil.example/x`} {
		t.Run(endpoint, func(t *testing.T) {
			t.Parallel()
			var gotHost string
			ac := newTestClient("https://workspace.example")
			ac.HTTPClient.Transport = rawRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				gotHost = r.URL.Host
				return rawTestResponse(http.StatusOK, "application/json", `{}`), nil
			})

			_, err := ac.SendRawRequest(http.MethodGet, endpoint, nil, nil)

			require.NoError(t, err)
			assert.Equal(t, "workspace.example", gotHost)
		})
	}
}

func TestSendRawRequest_CustomAuthorizationDoesNotReplayWithManagedToken(t *testing.T) {
	renewals := stubTokenRenewal(t, "fresh-managed")
	var tokens, bodies []string
	ac := newBearerTestClient("https://workspace.example", "managed")
	ac.HTTPClient.Transport = rawRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		tokens = append(tokens, r.Header.Get("Authorization"))
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		if len(tokens) == 1 {
			return rawTestResponse(http.StatusUnauthorized, "application/json", `{"detail":"expired"}`), nil
		}
		return rawTestResponse(http.StatusOK, "application/json", `{"unexpected":true}`), nil
	})

	response, err := ac.SendRawRequest(http.MethodPost, "/x", bytes.NewReader([]byte(`{"value":1}`)), http.Header{"Authorization": []string{"Bearer custom-token"}})

	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
	assert.Equal(t, []string{"Bearer custom-token"}, tokens)
	assert.Equal(t, []string{`{"value":1}`}, bodies)
	assert.Equal(t, 0, *renewals)
}

func TestSendRawRequest_RenewalPreservesCustomUserAgent(t *testing.T) {
	stubTokenRenewal(t, "fresh")
	var agents []string
	ac := newBearerTestClient("https://workspace.example", "stale")
	ac.UserAgent = "default-agent"
	ac.HTTPClient.Transport = rawRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		agents = append(agents, r.Header.Get("User-Agent"))
		if len(agents) == 1 {
			return rawTestResponse(http.StatusUnauthorized, "application/json", `{"detail":"expired"}`), nil
		}
		return rawTestResponse(http.StatusOK, "application/json", `{}`), nil
	})

	response, err := ac.SendRawRequest(http.MethodGet, "/x", nil, http.Header{"User-Agent": []string{"custom-agent"}})

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, []string{"custom-agent", "custom-agent"}, agents)
}
