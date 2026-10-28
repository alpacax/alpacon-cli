package utils

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
)

// Synthetic error_code values: the CLI's own, for failures the server sent no code for.
const (
	ErrorCodeUsage                = "usage_error" // local validation that never reached the server
	ErrorCodeNotLoggedIn          = "not_logged_in"
	ErrorCodeConfirmationRequired = "confirmation_required"
	ErrorCodeNetwork              = "network_error"
	ErrorCodeNotApproved          = "not_approved"
	ErrorCodeCLI                  = "cli_error"
)

// ErrNotLoggedIn marks a missing config file; a malformed or unreadable one stays cli_error.
var ErrNotLoggedIn = errors.New("not logged in")

type notLoggedInError struct{ err error }

func (e notLoggedInError) Error() string   { return e.err.Error() }
func (e notLoggedInError) Unwrap() []error { return []error{ErrNotLoggedIn, e.err} }

// MarkNotLoggedIn tags err with ErrNotLoggedIn and leaves its message as it was.
func MarkNotLoggedIn(err error) error {
	return notLoggedInError{err: err}
}

type usageError struct{ err error }

func (e usageError) Error() string       { return e.err.Error() }
func (e usageError) Unwrap() error       { return e.err }
func (e usageError) ErrorCode() string   { return ErrorCodeUsage }
func (e usageError) ErrorSource() string { return "" }

// MarkUsageError tags err as a command-line mistake, so ClassifyError reports usage_error for it.
func MarkUsageError(err error) error {
	if err == nil {
		return nil
	}
	return usageError{err: err}
}

// IsUsageError reports whether MarkUsageError tagged err or an error it wraps; a server's
// usage_error code does not count, since only this process knows its command line was wrong.
func IsUsageError(err error) bool {
	return errors.As(err, new(usageError))
}

var serverCodeShape = regexp.MustCompile(`^[a-z0-9_]+$`)

// HTTPErrorCode is the synthetic code for a server or proxy answer that carried none.
func HTTPErrorCode(status int) string {
	return fmt.Sprintf("http_%d", status)
}

// ClassifyError picks the error_code for err: the server's code, else http_<status>, else
// network_error for a transport failure, else cli_error. A missing config file is not_logged_in.
func ClassifyError(err error) string {
	if code := StructuredErrorCode(err); code != "" {
		return code
	}
	if code, _ := ParseErrorResponse(err); serverCodeShape.MatchString(code) {
		return code
	}
	if errors.Is(err, ErrNotLoggedIn) {
		return ErrorCodeNotLoggedIn
	}
	if status := HTTPStatusCode(err); status > 0 {
		return HTTPErrorCode(status)
	}
	if isTransportError(err) {
		return ErrorCodeNetwork
	}
	return ErrorCodeCLI
}

// isTransportError reports a request that never got an HTTP answer. It excludes context.Canceled,
// the caller's choice, and a bare net.Error, which syscall.Errno also satisfies.
func isTransportError(err error) bool {
	var (
		dnsErr    *net.DNSError
		unknownCA x509.UnknownAuthorityError
		hostErr   x509.HostnameError
		invalid   x509.CertificateInvalidError
		verifyErr *tls.CertificateVerificationError
		recordErr tls.RecordHeaderError
	)
	if errors.Is(err, context.Canceled) {
		return false
	}
	return isRemoteOpError(err) || errors.As(err, &dnsErr) || isRemoteURLError(err) ||
		errors.As(err, &unknownCA) || errors.As(err, &hostErr) || errors.As(err, &invalid) ||
		errors.As(err, &verifyErr) || errors.As(err, &recordErr) ||
		errors.Is(err, context.DeadlineExceeded)
}

// isRemoteOpError excludes local ops such as listen, which say nothing about a peer.
func isRemoteOpError(err error) bool {
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		return false
	}
	switch opErr.Op {
	case "dial", "read", "write", "proxyconnect":
		return true
	}
	return false
}

// isRemoteURLError excludes url.Parse failures, which never left the process.
func isRemoteURLError(err error) bool {
	var urlErr *url.Error
	return errors.As(err, &urlErr) && urlErr.Op != "parse"
}

type fieldsCarrier interface {
	ErrorFields() map[string]any
}

func serverErrorFields(err error) map[string]any {
	for e := range errorChain(err) {
		if carrier, ok := e.(fieldsCarrier); ok {
			if fields := carrier.ErrorFields(); len(fields) > 0 {
				return fields
			}
		}
	}
	return nil
}
