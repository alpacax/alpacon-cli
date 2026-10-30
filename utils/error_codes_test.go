package utils

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
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
		{name: "field error on a code field is not a code", err: &statusWrap{err: errors.New("code: This field is required."), status: 400}, want: "http_400"},
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
		{name: "hostname mismatch", err: fmt.Errorf("tls: %w", x509.HostnameError{Certificate: &x509.Certificate{}, Host: "x.example"}), want: ErrorCodeNetwork},
		{name: "invalid certificate", err: fmt.Errorf("tls: %w", x509.CertificateInvalidError{Reason: x509.Expired}), want: ErrorCodeNetwork},
		{name: "certificate verification", err: fmt.Errorf("tls: %w", &tls.CertificateVerificationError{Err: errors.New("bad")}), want: ErrorCodeNetwork},
		{name: "tls record header", err: fmt.Errorf("tls: %w", tls.RecordHeaderError{}), want: ErrorCodeNetwork},
		{name: "deadline exceeded without status", err: fmt.Errorf("wait: %w", context.DeadlineExceeded), want: ErrorCodeNetwork},
		{name: "transport error under an HTTP status stays http", err: &statusWrap{err: dial, status: 503}, want: "http_503"},
		{name: "dns error", err: fmt.Errorf("lookup: %w", &net.DNSError{Err: "no such host", Name: "x.example"}), want: ErrorCodeNetwork},
		{name: "local file open failure", err: &fs.PathError{Op: "open", Path: "/nope", Err: syscall.ENOENT}, want: ErrorCodeCLI},
		{name: "wrapped permission error", err: fmt.Errorf("read key: %w", &fs.PathError{Op: "open", Path: "/k", Err: syscall.EACCES}), want: ErrorCodeCLI},
		{name: "bare errno", err: syscall.ECONNRESET, want: ErrorCodeCLI},
		{name: "no login on disk", err: fmt.Errorf("configuration file not found: %w", ErrNotLoggedIn), want: ErrorCodeNotLoggedIn},
		{name: "server code wins over a missing login", err: errors.Join(ErrNotLoggedIn, &fakeAPIError{code: "rbac_permission_required", status: 403}), want: "rbac_permission_required"},
		{name: "context canceled is not a network failure", err: context.Canceled, want: ErrorCodeCLI},
		{name: "marked usage error", err: MarkUsageError(errors.New("bad flag")), want: ErrorCodeUsage},
		{name: "marked usage error under a wrapper", err: fmt.Errorf("while running: %w", MarkUsageError(errors.New("bad flag"))), want: ErrorCodeUsage},
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
			got := serverErrorFields(tt.err)
			if tt.want == nil {
				assert.Nil(t, got)
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMarkUsageErrorKeepsItsCauseAndMessage(t *testing.T) {
	t.Parallel()
	cause := errors.New("bad flag")

	err := fmt.Errorf("while running: %w", MarkUsageError(cause))

	require.ErrorIs(t, err, cause)
	assert.Equal(t, "while running: bad flag", err.Error())
}

func TestMarkUsageErrorOfNilIsNil(t *testing.T) {
	t.Parallel()
	assert.NoError(t, MarkUsageError(nil))
}

func TestIsUsageErrorAnswersOnlyForAMarkedError(t *testing.T) {
	t.Parallel()
	marked := MarkUsageError(errors.New("bad flag"))
	server := &fakeAPIError{code: ErrorCodeUsage, status: 400}
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "marked", err: marked, want: true},
		{name: "marked under a wrapper", err: fmt.Errorf("while running: %w", marked), want: true},
		{name: "marked joined after a server error", err: errors.Join(server, marked), want: true},
		{name: "marked joined before a server error", err: errors.Join(marked, server), want: true},
		{name: "a server answer coded usage_error", err: server},
		{name: "a message that reads usage_error", err: errors.New("code: usage_error; source: x")},
		{name: "nil", err: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, IsUsageError(tt.err))
		})
	}
}
