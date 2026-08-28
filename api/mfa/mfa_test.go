package mfa

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckMFACompletion_Completed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, mfaCompletionURL, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"completed": true}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	completed, err := CheckMFACompletion(ac)
	assert.NoError(t, err)
	assert.True(t, completed)
}

func TestCheckMFACompletion_NotCompleted(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"completed": false}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	completed, err := CheckMFACompletion(ac)
	assert.NoError(t, err)
	assert.False(t, completed)
}

func TestCheckMFACompletion_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"detail": "internal server error"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient: ts.Client(),
		BaseURL:    ts.URL,
	}

	_, err := CheckMFACompletion(ac)
	assert.Error(t, err)
}

func mfaLinkServer(t *testing.T, body string) (*client.AlpaconClient, *url.Values) {
	t.Helper()
	var query url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, mfaURL+"/", r.URL.Path)
		query = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)

	return &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL, WorkspaceName: "my-workspace"}, &query
}

func TestGetMFALink_ReturnsTheURLAndScopesItToTheServer(t *testing.T) {
	ac, query := mfaLinkServer(t, `{"mfa_url": "https://example.com/mfa"}`)

	link, err := GetMFALink(ac, "server-id")

	assert.NoError(t, err)
	assert.Equal(t, "https://example.com/mfa", link)
	assert.Equal(t, "cli", query.Get("location"))
	assert.Equal(t, "server-id", query.Get("server"))
	assert.Equal(t, "my-workspace", query.Get("workspace"))
}

// It used to yield ("", nil), so callers opened a browser on a blank link.
func TestGetMFALink_MalformedBodyIsAnError(t *testing.T) {
	ac, _ := mfaLinkServer(t, `not json`)

	link, err := GetMFALink(ac, "server-id")

	assert.Error(t, err)
	assert.Empty(t, link)
}

func TestGetMFALink_EmptyURLIsAnError(t *testing.T) {
	ac, _ := mfaLinkServer(t, `{"mfa_url": ""}`)

	link, err := GetMFALink(ac, "server-id")

	assert.Error(t, err)
	assert.Empty(t, link)
}

func TestGetWorkspaceSecurityMFALink_SendsNoServerScope(t *testing.T) {
	ac, query := mfaLinkServer(t, `{"mfa_url": "https://example.com/mfa"}`)

	link, err := GetWorkspaceSecurityMFALink(ac)

	assert.NoError(t, err)
	assert.Equal(t, "https://example.com/mfa", link)
	assert.Equal(t, "cli", query.Get("location"))
	assert.Equal(t, "my-workspace", query.Get("workspace"))
	assert.NotContains(t, *query, "server")
}

func TestGetMFALink_ServerErrorIsAnError(t *testing.T) {
	reached := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"detail": "internal server error"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient:    ts.Client(),
		BaseURL:       ts.URL,
		WorkspaceName: "my-workspace",
	}

	link, err := GetMFALink(ac, "server-id")

	assert.Error(t, err)
	assert.Empty(t, link)
	assert.True(t, reached, "the error must come from the server, not from a guard before the request")
}

func TestErrorCallbacks_WiresEveryField(t *testing.T) {
	ac := &client.AlpaconClient{}
	retried := false

	cb := ErrorCallbacks(ac, func() error { retried = true; return nil })

	assert.NotNil(t, cb.OnMFARequired)
	assert.NotNil(t, cb.OnUsernameRequired)
	// A nil CheckMFACompleted silently drops every caller onto the legacy
	// retry loop in utils.HandleCommonErrors—no compile error, no failure.
	assert.NotNil(t, cb.CheckMFACompleted)
	assert.NotNil(t, cb.RefreshToken)

	require.NotNil(t, cb.RetryOperation)
	assert.NoError(t, cb.RetryOperation())
	assert.True(t, retried, "RetryOperation must be the closure passed in")
}

func TestGetMFALink_EmptyWorkspaceIsAnErrorBeforeAnyRequest(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	link, err := GetMFALink(ac, "server-id")

	assert.Error(t, err)
	assert.Empty(t, link)
	assert.False(t, called, "an empty workspace must not reach the server")
}

func TestGetMFALinkByServerName_NamesTheWorkspaceTheClientIsPinnedTo(t *testing.T) {
	// A regression guard, not a claim about today's code: an empty HOME leaves
	// no config, so a second read finding its way back in fails here.
	t.Setenv("HOME", t.TempDir())

	var query url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == mfaURL+"/" {
			query = r.URL.Query()
			_, _ = w.Write([]byte(`{"mfa_url": "https://example.com/mfa"}`))
			return
		}
		_, _ = w.Write([]byte(`{"count": 1, "results": [{"id": "server-id"}]}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{
		HTTPClient:    ts.Client(),
		BaseURL:       ts.URL,
		WorkspaceName: "pinned-ws",
	}

	link, err := GetMFALinkByServerName(ac, "my-server")

	require.NoError(t, err)
	assert.Equal(t, "https://example.com/mfa", link)
	assert.Equal(t, "server-id", query.Get("server"))
	assert.Equal(t, "pinned-ws", query.Get("workspace"))
}

func TestGetMFALinkByServerName_EmptyWorkspaceCostsNoRoundTrip(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}

	link, err := GetMFALinkByServerName(ac, "my-server")

	assert.Error(t, err)
	assert.Empty(t, link)
	assert.False(t, called, "the name lookup must not run when no workspace can be named")
}
