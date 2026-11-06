package client

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/alpacax/alpacon-cli/pkg/aiagent"
	"github.com/alpacax/alpacon-cli/pkg/httpclient"
	"github.com/alpacax/alpacon-cli/utils"
)

func (ac *AlpaconClient) SendRawRequest(method, path string, body io.Reader, header http.Header) (*RawResponse, error) {
	base, err := url.Parse(ac.BaseURL)
	if err != nil || base.Host == "" {
		return nil, fmt.Errorf("invalid workspace URL")
	}
	path, err = NormalizeRawEndpoint(path)
	if err != nil {
		return nil, err
	}
	req, err := ac.createRequest(method, path, body)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(req.URL.Host, base.Host) || !strings.EqualFold(req.URL.Scheme, base.Scheme) {
		return nil, fmt.Errorf("endpoint must be a path on the current workspace")
	}
	if err := ValidateRawRequestHeaders(req.Method, header); err != nil {
		return nil, err
	}
	for name, values := range header {
		setHeaderValues(req.Header, name, values)
	}
	// A caller-supplied header does not replace the declaration: an agent could
	// otherwise send itself as a person through a raw request.
	aiagent.FromEnvironment().SetHeaders(req.Header)
	return ac.sendRawRequest(req, header)
}

// sendRawRequest sends req and, when the server reports the access token stale,
// renews it and sends the request once more, mirroring sendRequest for the raw path.
func (ac *AlpaconClient) sendRawRequest(req *http.Request, header http.Header) (*RawResponse, error) {
	response, err := ac.rawRoundTrip(req)
	if err != nil {
		return nil, err
	}
	// A caller-supplied Authorization is never renewed: replaying would swap in the CLI's own token.
	if hasHeader(header, "Authorization") {
		return response, nil
	}
	var authErr error
	if response.StatusCode == http.StatusUnauthorized {
		authErr = withStatus(checkAuthStatus(response.StatusCode, response.Body), response.StatusCode)
	}
	retry, ok := ac.renewedRequest(req, authErr)
	if !ok {
		return response, nil
	}
	for name, values := range header {
		if !strings.EqualFold(name, "User-Agent") {
			continue
		}
		setHeaderValues(retry.Header, name, values)
	}
	return ac.rawRoundTrip(retry)
}

// hasHeader reports whether header carries name, case-insensitively.
func hasHeader(header http.Header, name string) bool {
	for candidate := range header {
		if strings.EqualFold(candidate, name) {
			return true
		}
	}
	return false
}

// setHeaderValues replaces name's values in h with values.
func setHeaderValues(h http.Header, name string, values []string) {
	h.Del(name)
	for _, value := range values {
		h.Add(name, value)
	}
}

// ValidateRawRequestHeaders checks special headers that net/http does not send from Request.Header.
func ValidateRawRequestHeaders(method string, header http.Header) error {
	for name := range header {
		if strings.EqualFold(name, "Host") || strings.EqualFold(name, "Transfer-Encoding") || strings.EqualFold(name, "Trailer") || strings.EqualFold(name, "Content-Length") {
			return fmt.Errorf("unsupported request header: %s", name)
		}
	}
	return nil
}

func NormalizeRawEndpoint(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("endpoint must be a path on the current workspace")
	}
	parsed, err := url.Parse(path)
	// url.Parse accepts a raw '#' (even an empty fragment) and a raw space or bad
	// %-escape in the query, all of which reach http.NewRequest unencoded and malform the wire.
	if err != nil || parsed.IsAbs() || parsed.Host != "" || strings.HasPrefix(path, "//") || strings.Contains(path, "#") || hasRawSpaceOrBadEscape(parsed.RawQuery) {
		return "", fmt.Errorf("endpoint must be a path on the current workspace")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path, nil
}

// hasRawSpaceOrBadEscape rejects what would reach the wire unencoded: a literal
// space, or a '%' not followed by two hex digits. '+', ';', and '&' are untouched.
func hasRawSpaceOrBadEscape(query string) bool {
	isHex := func(c byte) bool {
		return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
	}
	for i := 0; i < len(query); i++ {
		switch query[i] {
		case ' ':
			return true
		case '%':
			if i+2 >= len(query) || !isHex(query[i+1]) || !isHex(query[i+2]) {
				return true
			}
			i += 2
		}
	}
	return false
}

func (ac *AlpaconClient) rawRoundTrip(req *http.Request) (*RawResponse, error) {
	resp, err := httpclient.StopAtCrossOrigin(ac.HTTPClient).Do(req)
	if err != nil {
		return nil, utils.WithRedirectStatus(err, resp)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return &RawResponse{Status: resp.Status, StatusCode: resp.StatusCode, Proto: resp.Proto, Header: resp.Header.Clone(), Body: respBody}, nil
}
