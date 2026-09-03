package testutil

import (
	"io"
	"net/http"
	"strings"
)

// StubTransport answers every request from fn, with no socket and no server
// goroutine. A poll loop under testing/synctest needs that: the fake clock only
// advances while every goroutine in the bubble is durably blocked, and a real
// network read never counts as durably blocked.
type StubTransport struct {
	Respond func(*http.Request) (status int, body string)
}

// RoundTrip implements http.RoundTripper.
func (t StubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	status, body := t.Respond(req)
	return &http.Response{
		StatusCode:    status,
		Status:        http.StatusText(status),
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}, nil
}

// StubBaseURL is the BaseURL to pin on a client whose transport is stubbed: it
// must parse, and it must never resolve.
const StubBaseURL = "http://stub.invalid"

// StubClient returns an http.Client that answers from fn.
func StubClient(fn func(*http.Request) (status int, body string)) *http.Client {
	return &http.Client{Transport: StubTransport{Respond: fn}}
}
