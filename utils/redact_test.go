package utils

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const signedURL = "https://bucket.example.com/path/file.zip?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Signature=secret123#frag"

func TestRedactURL(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "https://bucket.example.com/path/file.zip", RedactURL(signedURL))
	assert.Equal(t, "wss://host/ws", RedactURL("wss://user:pw@host/ws?token=secret"))
	assert.Equal(t, "not a url", RedactURL("not a url?token=secret"))
	// A URL that does not parse still loses its query and its userinfo.
	assert.Equal(t, "https://host/%zz", RedactURL("https://user:password@host/%zz?token=secret"))
	assert.Equal(t, "https://host", RedactURL("https://user:password@host?token=secret"))
}

func TestRedactURLError(t *testing.T) {
	t.Parallel()
	op := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	cases := map[string]error{
		"url error":         &url.Error{Op: "Get", URL: signedURL, Err: op},
		"wrapped url error": fmt.Errorf("network error while downloading: %w", &url.Error{Op: "Get", URL: signedURL, Err: op}),
	}
	for name, err := range cases {
		t.Run(name, func(t *testing.T) {
			got := RedactURLError(err)

			assert.NotContains(t, got.Error(), "secret123")
			assert.NotContains(t, got.Error(), "X-Amz-Signature")
			assert.Contains(t, got.Error(), "https://bucket.example.com/path/file.zip")
			assert.Contains(t, got.Error(), "connection refused")
			var gotOp *net.OpError
			require.ErrorAs(t, got, &gotOp)
			var gotURL *url.Error
			require.ErrorAs(t, got, &gotURL)
			assert.NotContains(t, gotURL.URL, "secret123")
		})
	}

	t.Run("deadline stays matchable", func(t *testing.T) {
		got := RedactURLError(&url.Error{Op: "Get", URL: signedURL, Err: context.DeadlineExceeded})
		require.ErrorIs(t, got, context.DeadlineExceeded)
		assert.NotContains(t, got.Error(), "secret123")
	})
	t.Run("others pass through", func(t *testing.T) {
		plain := errors.New("boom")
		assert.Equal(t, plain, RedactURLError(plain))
		require.NoError(t, RedactURLError(nil))
		clean := &url.Error{Op: "Get", URL: "https://h/p", Err: plain}
		assert.Equal(t, error(clean), RedactURLError(clean))
	})
}

// net/http puts the Location it could not parse, quoted, in the inner error of
// the *url.Error, so the URL appears twice in the message.
func TestRedactURLError_RedactsAQuotedURLInTheInnerError(t *testing.T) {
	t.Parallel()
	inner := fmt.Errorf("failed to parse Location header %q: invalid URL escape", "https://bucket.example.com/f?X-Amz-Signature=secret123&x=%zz")
	err := &url.Error{Op: "Get", URL: "https://api.example.com/download/", Err: inner}

	got := RedactURLError(err)

	assert.NotContains(t, got.Error(), "secret123")
	assert.Contains(t, got.Error(), "failed to parse Location header")
	require.ErrorIs(t, got, inner)
}

// The wrapper reports a timeout the way the *url.Error it replaces does.
func TestRedactURLError_KeepsTimeoutBehavior(t *testing.T) {
	t.Parallel()
	got := RedactURLError(&url.Error{Op: "Get", URL: signedURL, Err: context.DeadlineExceeded})

	assert.True(t, os.IsTimeout(got))
	var netErr net.Error
	require.ErrorAs(t, got, &netErr)
	assert.True(t, netErr.Timeout())
}

// The websocket channel token is a path segment, so these keep the host only.
func TestRedactURLHostOnly(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "wss://proxy.example.com", RedactURLHostOnly("wss://proxy.example.com/ws/websh/sid/cid/PATHTOKEN/?q=1"))
	assert.Equal(t, "wss://proxy.example.com", RedactURLHostOnly("wss://user:pw@proxy.example.com/ws/websh/sid/cid/PATHTOKEN/%zz"))

	got := RedactURLErrorHostOnly(&url.Error{Op: "parse", URL: "wss://proxy.example.com/ws/websh/sid/cid/PATHTOKEN/%zz", Err: errors.New("invalid URL escape")})
	assert.NotContains(t, got.Error(), "PATHTOKEN")
	assert.Contains(t, got.Error(), "invalid URL escape")
}
