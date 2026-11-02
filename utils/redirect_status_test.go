package utils

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithRedirectStatus(t *testing.T) {
	t.Parallel()
	cause := errors.New("stopped after 10 redirects")
	tests := []struct {
		name       string
		resp       *http.Response
		wantStatus int
		wantCode   string
	}{
		{name: "redirect response is kept", resp: &http.Response{StatusCode: http.StatusFound}, wantStatus: http.StatusFound, wantCode: "http_302"},
		{name: "no response leaves the error alone", resp: nil, wantStatus: 0, wantCode: ErrorCodeCLI},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := WithRedirectStatus(cause, tt.resp)

			require.ErrorIs(t, err, cause)
			assert.Equal(t, tt.wantStatus, HTTPStatusCode(err))
			assert.Equal(t, tt.wantCode, ClassifyError(err))
		})
	}
	assert.NoError(t, WithRedirectStatus(nil, &http.Response{StatusCode: http.StatusFound}))
}
