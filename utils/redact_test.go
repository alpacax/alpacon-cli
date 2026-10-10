package utils

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
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
