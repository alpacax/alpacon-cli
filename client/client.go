package client

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api/auth0"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	checkPrivilegesURL = "/api/iam/users/-"
)

type apiError struct {
	message string
	code    string
	source  string
}

func NewAlpaconAPIClient() (*AlpaconClient, error) {
	validConfig, err := config.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("configuration file not found or invalid: %v. Please run 'alpacon login' to configure your connection", err)
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion:         tls.VersionTLS12,
				InsecureSkipVerify: validConfig.Insecure,
			},
		},
	}

	client := &AlpaconClient{
		HTTPClient:  httpClient,
		BaseURL:     validConfig.WorkspaceURL,
		Token:       validConfig.Token,
		AccessToken: validConfig.AccessToken,
		UserAgent:   utils.GetUserAgent(),
	}

	if isAccessTokenExpired(validConfig) {
		spinner := utils.NewSpinner("Refreshing access token...")
		spinner.Start()
		tokenRes, err := auth0.RefreshAccessToken(validConfig.WorkspaceURL, httpClient, validConfig.RefreshToken)
		spinner.Stop()
		if err != nil {
			return nil, fmt.Errorf("failed to refresh access token: %v. Your session may have expired completely. Please run 'alpacon login' to authenticate again", err)
		}

		client.AccessToken = tokenRes.AccessToken
	}

	return client, nil
}

func (ac *AlpaconClient) LoadCurrentUser() error {
	ac.loadOnce.Do(func() {
		body, err := ac.SendGetRequest(checkPrivilegesURL)
		if err != nil {
			ac.loadErr = err
			return
		}
		var resp CheckPrivilegesResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			ac.loadErr = err
			return
		}
		ac.Privileges = getUserPrivileges(resp.IsStaff, resp.IsSuperuser)
		ac.Username = strings.TrimSpace(resp.Username)
	})
	return ac.loadErr
}

func getUserPrivileges(isStaff, isSuperuser bool) string {
	if isSuperuser {
		return "superuser"
	}
	if isStaff {
		return "staff"
	}
	return "general"
}

// checkAuthStatus prefers the server-provided reason and only suggests
// re-login for a bare authentication failure—never for a coded condition such
// as MFA-required or a policy denial, which re-login does not resolve. The
// error code is always preserved on the returned error so downstream handlers
// (MFA flow, WorkSession gate) can still route on it.
func checkAuthStatus(statusCode int, body []byte) error {
	if statusCode != http.StatusUnauthorized && statusCode != http.StatusForbidden {
		return nil
	}
	detail, code, source, hasDetail := parseAuthStatusErrorPayload(body)
	return newAPIError(authStatusMessage(statusCode, code, detail, hasDetail), code, source)
}

// authStatusMessage renders the user-facing message for a 401/403. It prefers
// the server's human detail; absent that, a known structured code maps to a
// clear message. A code-less 401 is the only case that suggests re-login—an
// authenticated user who merely needs MFA, or who hit a policy denial, must not
// be told to log in again.
func authStatusMessage(statusCode int, code, detail string, hasDetail bool) string {
	if hasDetail {
		if statusCode == http.StatusUnauthorized && code == "" {
			return fmt.Sprintf("%s (run 'alpacon login' if your session has expired)", detail)
		}
		return detail
	}
	if msg, ok := authStatusCodeMessage(code); ok {
		return msg
	}
	if statusCode == http.StatusUnauthorized {
		if code == "" {
			return "authentication failed: please run 'alpacon login' again"
		}
		// A coded 401 is a deliberate server decision, not a stale token; do not
		// mislabel it as an authentication failure or suggest re-login.
		// Deliberately information-light: do not interpolate the raw code into the
		// message—callers read it via ErrorCode(), and embedding it would break the
		// no-raw-code contract that TestSendRequest_403CodeWithoutDetailKeepsCodeSource guards.
		return "request denied by server"
	}
	return "permission denied: you do not have the required privileges for this action"
}

// authStatusCodeMessage maps structured server codes that arrive on a 401/403
// without a human detail to a clear, actionable message.
func authStatusCodeMessage(code string) (string, bool) {
	switch code {
	case utils.AuthMFARequired:
		return "multi-factor authentication required—complete MFA to continue", true
	}
	return "", false
}

// parseAuthStatusErrorPayload returns ok=true only with a clean "detail" message;
// code/source are returned regardless so the WorkSession gate can route to exit 3.
func parseAuthStatusErrorPayload(body []byte) (message string, code string, source string, ok bool) {
	message, code, source, ok = parseAPIErrorPayload(body)
	if !ok || message == "" {
		return "", code, source, false
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", code, source, false
	}
	if stringField(parsed, "detail") == "" {
		return "", code, source, false
	}
	return message, code, source, true
}

func (ac *AlpaconClient) SetWebsocketHeader() http.Header {
	headers := http.Header{}
	headers.Set("Origin", ac.BaseURL)
	headers.Set("User-Agent", ac.UserAgent)

	return headers
}

func (ac *AlpaconClient) setHTTPHeader(req *http.Request) *http.Request {
	req.Header.Set("User-Agent", ac.UserAgent)
	if ac.AccessToken != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", ac.AccessToken))
	} else if ac.Token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("token=\"%s\"", ac.Token))
	}

	return req
}

func (ac *AlpaconClient) createRequest(method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, ac.BaseURL+url, body)
	if err != nil {
		return nil, err
	}

	req = ac.setHTTPHeader(req)
	if method == http.MethodPost || method == http.MethodPatch || method == http.MethodPut {
		req.Header.Add("Content-Type", "application/json")
	}

	return req, nil
}

// readJSONResponse reads the body, surfaces 401/403 with server detail,
// and rejects non-JSON content types. Other status-code enforcement is
// left to the caller.
func readJSONResponse(resp *http.Response) ([]byte, error) {
	body, readErr := io.ReadAll(resp.Body)
	if err := checkAuthStatus(resp.StatusCode, body); err != nil {
		return nil, err
	}
	if readErr != nil {
		return nil, readErr
	}

	// Empty content type is allowed for responses without content (e.g. PATCH).
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.Contains(ct, "application/json") {
		return nil, fmt.Errorf("unexpected response from server (HTTP %d, Content-Type: %s)", resp.StatusCode, ct)
	}
	return body, nil
}

func (ac *AlpaconClient) sendRequest(req *http.Request) ([]byte, error) {
	resp, err := ac.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := readJSONResponse(resp)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, parseAPIError(respBody)
	}

	return respBody, nil
}

// Get Request to Alpacon Server
func (ac *AlpaconClient) SendGetRequest(url string) ([]byte, error) {
	req, err := ac.createRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return ac.sendRequest(req)
}

// POST Request to Alpacon Server
func (ac *AlpaconClient) SendPostRequest(url string, body any) ([]byte, error) {
	jsonValue, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := ac.createRequest(http.MethodPost, url, bytes.NewBuffer(jsonValue))
	if err != nil {
		return nil, err
	}
	return ac.sendRequest(req)
}

func (ac *AlpaconClient) SendDeleteRequest(url string) ([]byte, error) {
	req, err := ac.createRequest(http.MethodDelete, url, nil)
	if err != nil {
		return nil, err
	}
	return ac.sendRequest(req)
}

func (ac *AlpaconClient) SendPatchRequest(url string, body any) ([]byte, error) {
	jsonValue, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := ac.createRequest(http.MethodPatch, url, bytes.NewBuffer(jsonValue))
	if err != nil {
		return nil, err
	}
	return ac.sendRequest(req)
}

func (ac *AlpaconClient) SendMultipartStreamRequest(url, contentType string, body io.Reader, contentLength int64) ([]byte, error) {
	req, err := ac.createRequest(http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	if f, ok := body.(*os.File); ok {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		name := f.Name()
		req.GetBody = func() (io.ReadCloser, error) {
			return os.Open(name)
		}
	}
	req.Header.Set("Content-Type", contentType)
	if contentLength >= 0 {
		req.ContentLength = contentLength
	}

	resp, err := ac.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := readJSONResponse(resp)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, parseAPIError(respBody)
	}

	return respBody, nil
}

// SendGetRequestToURL sends a GET request to an absolute URL (e.g., an external service)
// using the client's authentication headers.
func (ac *AlpaconClient) SendGetRequestToURL(absoluteURL string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, absoluteURL, nil)
	if err != nil {
		return nil, err
	}
	req = ac.setHTTPHeader(req)
	return ac.sendRequest(req)
}

// SendGetRequestForDownload returns the raw *http.Response so callers can stream the body.
// Auth errors (401/403) are handled here; all other status codes are left to the caller.
func (ac *AlpaconClient) SendGetRequestForDownload(url string) (*http.Response, error) {
	req, err := ac.createRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := ac.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, checkAuthStatus(resp.StatusCode, body)
	}

	return resp, nil
}

func (ac *AlpaconClient) IsUsingHTTPS() (bool, error) {
	parsedURL, err := url.Parse(ac.BaseURL)
	if err != nil {
		return false, err
	}

	if parsedURL.Scheme == "https" {
		return true, nil
	}

	return false, nil
}

// RefreshToken refreshes the access token using the stored refresh token.
// Uses ac.BaseURL (not config's WorkspaceURL) to stay consistent with the client's target.
func (ac *AlpaconClient) RefreshToken() error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return err
	}
	tokenRes, err := auth0.RefreshAccessToken(ac.BaseURL, ac.HTTPClient, cfg.RefreshToken)
	if err != nil {
		return err
	}
	ac.AccessToken = tokenRes.AccessToken
	return nil
}

func isAccessTokenExpired(cfg config.Config) bool {
	if cfg.AccessToken == "" {
		return false
	}

	if cfg.AccessTokenExpiresAt == "" {
		return true
	}

	expireTime, err := time.Parse(time.RFC3339, cfg.AccessTokenExpiresAt)
	if err != nil {
		return true
	}

	return time.Now().After(expireTime.Add(-10 * time.Second))
}

func (e *apiError) Error() string {
	return e.message
}

func (e *apiError) ErrorCode() string {
	return e.code
}

func (e *apiError) ErrorSource() string {
	return e.source
}

func newAPIError(message, code, source string) error {
	return &apiError{message: message, code: code, source: source}
}

// parseAPIError extracts a human-readable error message from a JSON API error response.
// Handles common formats: {"detail": "..."}, {"field": ["error", ...]}, {"non_field_errors": ["..."]}
func parseAPIError(body []byte) error {
	message, code, source, ok := parseAPIErrorPayload(body)
	if ok {
		return newAPIError(message, code, source)
	}
	return errors.New(message)
}

func parseAPIErrorPayload(body []byte) (message string, code string, source string, ok bool) {
	raw := string(body)

	if strings.TrimSpace(raw) == "" {
		return "server returned an empty error response", "", "", false
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		// Not valid JSON (e.g., HTML error page) — return truncated
		return truncateBody(raw), "", "", false
	}

	code = stringField(parsed, "code")
	source = stringField(parsed, "source")

	// Case 1: {"detail": "..."}
	if detail := strings.TrimSpace(stringField(parsed, "detail")); detail != "" {
		return detail, code, source, true
	}

	// Case 2: field validation errors {"field": ["msg1", "msg2"], ...}
	// Sort keys for deterministic output order
	fields := make([]string, 0, len(parsed))
	for field := range parsed {
		fields = append(fields, field)
	}
	sort.Strings(fields)

	var messages []string
	for _, field := range fields {
		switch v := parsed[field].(type) {
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					if field == "non_field_errors" {
						messages = append(messages, s)
					} else {
						messages = append(messages, fmt.Sprintf("%s: %s", field, s))
					}
				}
			}
		case string:
			messages = append(messages, fmt.Sprintf("%s: %s", field, v))
		}
	}

	if len(messages) > 0 {
		return strings.Join(messages, "; "), code, source, true
	}

	// Fallback: return truncated raw body
	return truncateBody(raw), code, source, true
}

func stringField(values map[string]any, field string) string {
	value, _ := values[field].(string)
	return strings.TrimSpace(value)
}

func truncateBody(s string) string {
	const maxLen = 200
	if len(s) > maxLen {
		return s[:maxLen] + "... (truncated)"
	}
	return s
}
