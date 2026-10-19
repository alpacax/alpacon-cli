package utils

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeAPIError struct {
	code   string
	status int
	fields map[string]any
	retry  time.Duration
}

func (e *fakeAPIError) Error() string               { return "fake api error" }
func (e *fakeAPIError) ErrorCode() string           { return e.code }
func (e *fakeAPIError) ErrorSource() string         { return "" }
func (e *fakeAPIError) HTTPStatusCode() int         { return e.status }
func (e *fakeAPIError) ErrorFields() map[string]any { return e.fields }
func (e *fakeAPIError) RetryAfter() time.Duration   { return e.retry }

type statusWrap struct {
	err    error
	status int
}

func (e *statusWrap) Error() string       { return e.err.Error() }
func (e *statusWrap) Unwrap() error       { return e.err }
func (e *statusWrap) HTTPStatusCode() int { return e.status }

func TestClassifyError(t *testing.T) {
	t.Parallel()
	dial := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil", err: nil, want: ErrorCodeCLI},
		{name: "plain error", err: errors.New("boom"), want: ErrorCodeCLI},
		{name: "server code", err: &fakeAPIError{code: "rbac_permission_required", status: 403}, want: "rbac_permission_required"},
		{name: "server code wins over status", err: &fakeAPIError{code: "auth_mfa_required", status: 401}, want: "auth_mfa_required"},
		{name: "code only in message text", err: errors.New("denied; code: work_session_required; source: gate"), want: "work_session_required"},
		{name: "no code 404", err: &fakeAPIError{status: 404}, want: "http_404"},
		{name: "no code 502", err: fmt.Errorf("list: %w", &fakeAPIError{status: 502}), want: "http_502"},
		{name: "dial error", err: fmt.Errorf("request: %w", dial), want: ErrorCodeNetwork},
		{name: "url error", err: &url.Error{Op: "Get", URL: "https://x", Err: errors.New("EOF")}, want: ErrorCodeNetwork},
		{name: "url parse failure is local", err: &url.Error{Op: "parse", URL: "http://x/%zz", Err: errors.New("invalid URL escape")}, want: ErrorCodeCLI},
		{name: "listen failure is local", err: &net.OpError{Op: "listen", Net: "tcp", Err: errors.New("address already in use")}, want: ErrorCodeCLI},
		{name: "read from a peer", err: &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset")}, want: ErrorCodeNetwork},
		{name: "write to a peer", err: &net.OpError{Op: "write", Net: "tcp", Err: errors.New("broken pipe")}, want: ErrorCodeNetwork},
		{name: "proxy connect", err: &net.OpError{Op: "proxyconnect", Net: "tcp", Err: errors.New("refused")}, want: ErrorCodeNetwork},
		{name: "unknown authority", err: fmt.Errorf("tls: %w", x509.UnknownAuthorityError{}), want: ErrorCodeNetwork},
		{name: "deadline exceeded without status", err: fmt.Errorf("wait: %w", context.DeadlineExceeded), want: ErrorCodeNetwork},
		{name: "transport error under an HTTP status stays http", err: &statusWrap{err: dial, status: 503}, want: "http_503"},
		{name: "dns error", err: fmt.Errorf("lookup: %w", &net.DNSError{Err: "no such host", Name: "x.example"}), want: ErrorCodeNetwork},
		{name: "local file open failure", err: &fs.PathError{Op: "open", Path: "/nope", Err: syscall.ENOENT}, want: ErrorCodeCLI},
		{name: "wrapped permission error", err: fmt.Errorf("read key: %w", &fs.PathError{Op: "open", Path: "/k", Err: syscall.EACCES}), want: ErrorCodeCLI},
		{name: "bare errno", err: syscall.ECONNRESET, want: ErrorCodeCLI},
		{name: "context canceled is not a network failure", err: context.Canceled, want: ErrorCodeCLI},
		{name: "url error wrapping cancel is not a network failure", err: &url.Error{Op: "Get", URL: "https://x", Err: context.Canceled}, want: ErrorCodeCLI},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ClassifyError(tt.err))
		})
	}
}

func TestHTTPErrorCode(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "http_404", HTTPErrorCode(404))
}

func TestSyntheticErrorCodes(t *testing.T) {
	t.Parallel()
	assert.ElementsMatch(t, []string{
		"usage_error", "not_logged_in", "confirmation_required", "network_error",
		"remote_command_failed", "child_command_failed", "not_approved", "purpose_refused", "permission_not_held", "cli_error",
	}, SyntheticErrorCodes())
	for _, code := range SyntheticErrorCodes() {
		assert.False(t, strings.HasPrefix(code, "http_"), code)
	}
}

func TestServerErrorFields(t *testing.T) {
	t.Parallel()
	fields := map[string]any{
		"field_errors": map[string]any{"name": []any{"required"}},
		"gate":         "plan",
		"missing":      []any{"scope:a"},
		"axis":         "servers",
		"next":         "/api/x/",
	}
	tests := []struct {
		name string
		err  error
		want map[string]any
	}{
		{name: "nil", err: nil},
		{name: "plain", err: errors.New("x")},
		{name: "direct", err: &fakeAPIError{fields: fields}, want: fields},
		{name: "wrapped", err: fmt.Errorf("w: %w", &fakeAPIError{fields: fields}), want: fields},
		{name: "empty fields", err: &fakeAPIError{fields: map[string]any{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ServerErrorFields(tt.err)
			if tt.want == nil {
				assert.Nil(t, got)
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

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
