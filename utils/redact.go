package utils

import (
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// RedactURL keeps the scheme, host and path of a URL and drops everything that
// can carry a credential: the query (a presigned URL's signature), the fragment
// and any userinfo. A string that does not parse is cut at its first '?' or '#'.
func RedactURL(raw string) string {
	if parsed, err := url.Parse(raw); err == nil && parsed.Host != "" {
		return (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: parsed.Path}).String()
	}
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	// Userinfo sits in the authority, between "://" and the next '/'.
	if scheme := strings.Index(raw, "://"); scheme >= 0 {
		start := scheme + len("://")
		end := len(raw)
		if slash := strings.IndexByte(raw[start:], '/'); slash >= 0 {
			end = start + slash
		}
		if at := strings.LastIndexByte(raw[start:end], '@'); at >= 0 {
			raw = raw[:start] + raw[start+at+1:]
		}
	}
	return raw
}

// RedactURLHostOnly keeps only the scheme and host of a URL. Use it for a URL
// whose path carries a credential too, such as a websocket channel token.
func RedactURLHostOnly(raw string) string {
	redacted := RedactURL(raw)
	if parsed, err := url.Parse(redacted); err == nil && parsed.Host != "" {
		return (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host}).String()
	}
	// RedactURL left an unparsable string: cut it after the authority.
	if scheme := strings.Index(redacted, "://"); scheme >= 0 {
		start := scheme + len("://")
		if slash := strings.IndexByte(redacted[start:], '/'); slash >= 0 {
			return redacted[:start+slash]
		}
	}
	return redacted
}

// redactedURLError prints a transport error with its URL redacted. Its cause is
// the *url.Error with the redacted URL, so errors.Is and errors.As on what went
// wrong (a deadline, a *net.OpError) still work, and Timeout and Temporary
// answer as that *url.Error does. A typed wrapper above the *url.Error in the
// original chain is not carried over: only the redacted message and the
// *url.Error survive.
type redactedURLError struct {
	msg   string
	cause *url.Error
}

func (e *redactedURLError) Error() string   { return e.msg }
func (e *redactedURLError) Unwrap() error   { return e.cause }
func (e *redactedURLError) Timeout() bool   { return e.cause.Timeout() }
func (e *redactedURLError) Temporary() bool { return e.cause.Temporary() }

// quotedURL matches a quoted URL inside an error's text, as net/http quotes a
// Location header it could not parse.
var quotedURL = regexp.MustCompile(`"[A-Za-z][A-Za-z0-9+.-]*://[^"]*"`)

// RedactURLError rewrites a *url.Error in err's chain, which Go prints with the
// whole request URL, so that neither its message nor its fields carry the URL's
// query. A presigned URL's query holds a live signature, and an error message
// ends up in terminals and CI logs. A quoted URL inside the inner error's text,
// such as an unparsable redirect Location, is redacted too. An error without a
// *url.Error, or one with nothing to hide, is returned as is.
func RedactURLError(err error) error { return redactURLError(err, RedactURL) }

// RedactURLErrorHostOnly is RedactURLError for a URL whose path is a credential
// as well: it keeps only the scheme and host, so even a malformed URL that a
// parser quotes whole reveals nothing past the host.
func RedactURLErrorHostOnly(err error) error { return redactURLError(err, RedactURLHostOnly) }

func redactURLError(err error, redact func(string) string) error {
	var urlErr *url.Error
	if err == nil || !errors.As(err, &urlErr) {
		return err
	}
	redacted := *urlErr
	redacted.URL = redact(urlErr.URL)

	// url.Error prints the URL quoted, so replace the quoted form first.
	msg := strings.ReplaceAll(err.Error(), strconv.Quote(urlErr.URL), strconv.Quote(redacted.URL))
	msg = strings.ReplaceAll(msg, urlErr.URL, redacted.URL)
	msg = quotedURL.ReplaceAllStringFunc(msg, func(quoted string) string {
		raw, unquoteErr := strconv.Unquote(quoted)
		if unquoteErr != nil {
			raw = quoted[1 : len(quoted)-1]
		}
		return strconv.Quote(redact(raw))
	})
	if msg == err.Error() {
		return err
	}
	return &redactedURLError{msg: msg, cause: &redacted}
}
