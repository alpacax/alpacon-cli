package utils

import (
	"errors"
	"net/url"
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

// redactedURLError prints a transport error with its URL redacted. Its cause is
// the *url.Error with the redacted URL, so errors.Is and errors.As on what went
// wrong (a deadline, a *net.OpError) still work.
type redactedURLError struct {
	msg   string
	cause *url.Error
}

func (e *redactedURLError) Error() string { return e.msg }
func (e *redactedURLError) Unwrap() error { return e.cause }

// RedactURLError rewrites a *url.Error in err's chain, which Go prints with the
// whole request URL, so that neither its message nor its fields carry the URL's
// query. A presigned URL's query holds a live signature, and an error message
// ends up in terminals and CI logs. An error without a *url.Error, or whose URL
// has nothing to hide, is returned as is.
func RedactURLError(err error) error {
	var urlErr *url.Error
	if err == nil || !errors.As(err, &urlErr) {
		return err
	}
	redacted := *urlErr
	redacted.URL = RedactURL(urlErr.URL)
	if redacted.URL == urlErr.URL {
		return err
	}
	// url.Error prints the URL quoted, so replace the quoted form first.
	msg := strings.ReplaceAll(err.Error(), strconv.Quote(urlErr.URL), strconv.Quote(redacted.URL))
	msg = strings.ReplaceAll(msg, urlErr.URL, redacted.URL)
	return &redactedURLError{msg: msg, cause: &redacted}
}
