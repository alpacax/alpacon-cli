package ftp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/httpclient"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const presignedURL = "https://bucket.example.com/obj/file.bin?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=cred&X-Amz-Signature=secret123"

type failingTransport struct{ err error }

func (f failingTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, f.err }

func assertRedacted(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "X-Amz-Signature")
	assert.NotContains(t, err.Error(), "secret123")
	assert.NotContains(t, err.Error(), "X-Amz-Credential")
	assert.Contains(t, err.Error(), "bucket.example.com/obj/file.bin", "the path is kept")
}

// A transport error from a fetch or upload of a presigned URL must not print the
// URL's signed query, and its cause must stay matchable.
func TestObjectStorageTransferErrorsOmitTheSignedQuery(t *testing.T) {
	t.Parallel()
	causes := map[string]error{
		"deadline":   context.DeadlineExceeded,
		"connection": &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")},
	}
	for name, cause := range causes {
		client := &http.Client{Transport: failingTransport{err: cause}}
		match := func(t *testing.T, err error) {
			t.Helper()
			if name == "deadline" {
				require.ErrorIs(t, err, context.DeadlineExceeded)
				return
			}
			var opErr *net.OpError
			require.ErrorAs(t, err, &opErr)
		}

		t.Run("download fetch "+name, func(t *testing.T) {
			t.Parallel()
			_, err := fetchFromURLToFile(client, presignedURL, filepath.Join(t.TempDir(), "out"), 1)
			assertRedacted(t, err)
			match(t, err)
		})
		t.Run("upload put "+name, func(t *testing.T) {
			t.Parallel()
			err := uploadToS3(client, presignedURL, strings.NewReader("data"), 4)
			assertRedacted(t, err)
			match(t, err)
		})
	}
}

// A redirect the client refuses keeps its status on the error, behind the redaction.
func TestObjectStorageTransferErrorsKeepARefusedRedirectStatus(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, presignedURL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(ts.Close)

	tests := []struct {
		name     string
		transfer func(*http.Client) error
	}{
		{"download fetch", func(c *http.Client) error {
			_, err := fetchFromURLToFile(c, ts.URL, filepath.Join(t.TempDir(), "out"), 1)
			return err
		}},
		{"upload put", func(c *http.Client) error {
			return uploadToS3(c, ts.URL, strings.NewReader("data"), 4)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.transfer(httpclient.New(false))

			assertRedacted(t, err)
			assert.Equal(t, http.StatusTemporaryRedirect, utils.HTTPStatusCode(err))
		})
	}
}
