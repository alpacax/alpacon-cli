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

func TestWorkspaceErrorCallbacks_WiresEveryField(t *testing.T) {
	t.Parallel()
	ac := &client.AlpaconClient{}
	retried := false

	cb := WorkspaceErrorCallbacks(ac, func() error { retried = true; return nil })

	assert.NotNil(t, cb.OnMFARequired)
	// A nil CheckMFACompleted silently drops the caller onto the legacy retry loop in
	// utils.HandleCommonErrors—no compile error, no failure.
	assert.NotNil(t, cb.CheckMFACompleted)
	assert.NotNil(t, cb.RefreshToken)
	// The one field left nil on purpose—a workspace-level change takes no username,
	// unlike ErrorCallbacks.
	assert.Nil(t, cb.OnUsernameRequired)

	require.NotNil(t, cb.RetryOperation)
	assert.NoError(t, cb.RetryOperation())
	assert.True(t, retried, "RetryOperation must be the closure passed in")
}

func TestWorkspaceErrorCallbacks_MFALinkNamesTheWorkspaceTheClientIsPinnedTo(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")

	var query url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"mfa_url": "https://example.com/mfa"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL, WorkspaceName: "my-workspace"}
	cb := WorkspaceErrorCallbacks(ac, func() error { return nil })

	// A config read here instead of ac would drift from ac.BaseURL across an editor session.
	require.NoError(t, cb.OnMFARequired(""))
	assert.Equal(t, "my-workspace", query.Get("workspace"))
}

func TestWorkspaceErrorCallbacks_EmptyWorkspaceFailsInsteadOfPrintingALink(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")

	// A usable link, so dropping the guard fails both asserts rather than
	// tripping the parse error an empty body would raise anyway.
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"mfa_url": "https://example.com/mfa"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	cb := WorkspaceErrorCallbacks(ac, func() error { return nil })

	assert.Error(t, cb.OnMFARequired(""))
	assert.False(t, called, "an empty workspace must not reach the server")
}
