package workspace

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrorCallbacks_WiresEveryField(t *testing.T) {
	ac := &client.AlpaconClient{}
	retried := false

	cb := errorCallbacks(ac, "my-workspace", func() error { retried = true; return nil })

	assert.NotNil(t, cb.OnMFARequired)
	// A nil CheckMFACompleted silently drops both update commands onto the
	// legacy retry loop in utils.HandleCommonErrors—no compile error, no failure.
	assert.NotNil(t, cb.CheckMFACompleted)
	assert.NotNil(t, cb.RefreshToken)
	// These commands take no username, unlike mfa.ErrorCallbacks.
	assert.Nil(t, cb.OnUsernameRequired)

	require.NotNil(t, cb.RetryOperation)
	assert.NoError(t, cb.RetryOperation())
	assert.True(t, retried, "RetryOperation must be the closure passed in")
}

func TestErrorCallbacks_MFALinkNamesTheWorkspacePassedIn(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")

	var query url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"mfa_url": "https://example.com/mfa"}`))
	}))
	defer ts.Close()

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	cb := errorCallbacks(ac, "my-workspace", func() error { return nil })

	// The link must name the workspace the caller pinned the client to. Reading
	// config here instead would drift from ac.BaseURL across an editor session.
	require.NoError(t, cb.OnMFARequired(""))
	assert.Equal(t, "my-workspace", query.Get("workspace"))
}
