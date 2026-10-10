package client

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingTransport struct{ err error }

func (f failingTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, f.err }

// A transport error from a request whose URL carries a signed query, such as a
// download URL the server returned, must not print that query.
func TestTransportErrorsOmitTheQuery(t *testing.T) {
	t.Parallel()
	ac := &AlpaconClient{
		BaseURL:       "https://workspace.example",
		WorkspaceName: "my-workspace",
		HTTPClient:    &http.Client{Transport: failingTransport{err: context.DeadlineExceeded}},
	}
	const path = "/api/packages/download/?X-Amz-Signature=secret123&X-Amz-Credential=cred"

	tests := map[string]func() error{
		"get": func() error { _, err := ac.SendGetRequest(path); return err },
		"download": func() error {
			resp, err := ac.SendGetRequestForDownload(path)
			if resp != nil {
				_ = resp.Body.Close()
			}
			return err
		},
	}
	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := run()

			require.Error(t, err)
			assert.NotContains(t, err.Error(), "secret123")
			assert.NotContains(t, err.Error(), "X-Amz-Signature")
			require.ErrorIs(t, err, context.DeadlineExceeded)
		})
	}
}
