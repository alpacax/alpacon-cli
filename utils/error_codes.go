package utils

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
)

// Synthetic error_code values: the CLI's own, for failures the server sent no code for.
const (
	ErrorCodeUsage                = UsageErrorCode
	ErrorCodeNotLoggedIn          = "not_logged_in"
	ErrorCodeConfirmationRequired = "confirmation_required"
	ErrorCodeNetwork              = "network_error"
	ErrorCodeRemoteCommandFailed  = "remote_command_failed"
	ErrorCodeChildCommandFailed   = "child_command_failed"
	ErrorCodeNotApproved          = "not_approved"
	ErrorCodePurposeRefused       = "purpose_refused"
	ErrorCodePermissionNotHeld    = "permission_not_held"
	ErrorCodeCLI                  = "cli_error"
)

// HTTPErrorCode is the synthetic code for a server or proxy answer that carried none.
func HTTPErrorCode(status int) string {
	return fmt.Sprintf("http_%d", status)
}

// SyntheticErrorCodes lists the fixed synthetic codes; the http_<status> family is not included.
func SyntheticErrorCodes() []string {
	return []string{
		ErrorCodeUsage, ErrorCodeNotLoggedIn, ErrorCodeConfirmationRequired, ErrorCodeNetwork,
		ErrorCodeRemoteCommandFailed, ErrorCodeChildCommandFailed, ErrorCodeNotApproved,
		ErrorCodePurposeRefused, ErrorCodePermissionNotHeld, ErrorCodeCLI,
	}
}

// ClassifyError picks the error_code for err: the server's code, else network_error for a
// transport failure, else http_<status>, else cli_error.
func ClassifyError(err error) string {
	if err == nil {
		return ErrorCodeCLI
	}
	if code := StructuredErrorCode(err); code != "" {
		return code
	}
	if code, _ := ParseErrorResponse(err); code != "" {
		return code
	}
	status := HTTPStatusCode(err)
	if status == 0 && isTransportError(err) {
		return ErrorCodeNetwork
	}
	if status > 0 {
		return HTTPErrorCode(status)
	}
	return ErrorCodeCLI
}

// isTransportError is deliberately narrow: a request that never got an HTTP answer.
// context.Canceled is excluded as a caller's decision; bare net.Error is too, since syscall.Errno satisfies it.
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

func ServerErrorFields(err error) map[string]any {
	for e := range errorChain(err) {
		if carrier, ok := e.(fieldsCarrier); ok {
			if fields := carrier.ErrorFields(); len(fields) > 0 {
				return fields
			}
		}
	}
	return nil
}
