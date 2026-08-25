package client

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api/auth0"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	checkPrivilegesURL = "/api/iam/users/-"

	// bearerPrefix is the scheme setHTTPHeader signs an access token with. The
	// stale-token retry reads it back off the request to learn which token the
	// server rejected.
	bearerPrefix = "Bearer "
)

// refreshAccessToken is a test seam so a unit test can drive the stale-token
// retry without real Auth0 I/O.
var refreshAccessToken = (*AlpaconClient).refreshLocked

type apiError struct {
	message    string
	code       string
	source     string
	statusCode int
	// apiPayload records that the body was a JSON object—the shape every
	// alpacon-server error response has. A 401 without one was written by
	// something standing in front of the server, and the stale-token retry
	// reads this to tell the two apart.
	apiPayload bool
}

// statusError carries an HTTP status on errors that aren't *apiError (e.g. an
// HTML 404 page), so utils.HTTPStatusCode can still read it.
type statusError struct {
	err        error
	statusCode int
}

func (e *statusError) Error() string       { return e.err.Error() }
func (e *statusError) Unwrap() error       { return e.err }
func (e *statusError) HTTPStatusCode() int { return e.statusCode }

// retryAfterError carries the server's Retry-After hint so a retrying caller
// (the exec/websh poll loop) waits as long as it asked instead of guessing.
type retryAfterError struct {
	err        error
	retryAfter time.Duration
}

func (e *retryAfterError) Error() string             { return e.err.Error() }
func (e *retryAfterError) Unwrap() error             { return e.err }
func (e *retryAfterError) RetryAfter() time.Duration { return e.retryAfter }

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
	return &apiError{
		message:    authStatusMessage(statusCode, code, detail, hasDetail),
		code:       code,
		source:     source,
		apiPayload: isJSONObject(body),
	}
}

// isJSONObject reports whether body is a JSON object. alpacon-server renders
// every error as one, so anything else on a 401—an HTML page, a bare string,
// nothing at all—came from a proxy, a WAF or an mTLS gate ahead of it.
func isJSONObject(body []byte) bool {
	var parsed map[string]any
	return json.Unmarshal(body, &parsed) == nil && parsed != nil
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
	case utils.APITokenACLNotAllowed:
		return "denied by token access control—this token may not perform that action; review its rules with 'alpacon token acl'", true
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
	if accessToken := ac.accessToken(); accessToken != "" {
		req.Header.Set("Authorization", bearerPrefix+accessToken)
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
		return nil, withStatus(err, resp.StatusCode)
	}
	if readErr != nil {
		// The headers already carried a status; dropping it here would leave a
		// body cut mid-read indistinguishable from a request that never went out.
		return nil, withStatus(readErr, resp.StatusCode)
	}

	// Empty content type is allowed for responses without content (e.g. PATCH).
	if ct := resp.Header.Get("Content-Type"); ct != "" && !strings.Contains(ct, "application/json") {
		err := fmt.Errorf("unexpected response from server (HTTP %d, Content-Type: %s)", resp.StatusCode, ct)
		return nil, withRetryAfter(withStatus(err, resp.StatusCode), resp.Header)
	}
	return body, nil
}

// sendRequest sends req and, when the server reports the access token stale,
// renews it and sends the request once more.
//
// The renewal belongs here rather than in each polling loop because only the
// process-wide credential is stale, not the request: alpacon-server rejects a
// stale token in its permission layer (utils/api/permissions.py), before the
// view runs, so the first attempt changed nothing and the replay is the same
// request rather than a second one. A client built at startup and held for a
// half-hour approval wait would otherwise never re-enter the refresh that
// NewAlpaconAPIClient runs once.
func (ac *AlpaconClient) sendRequest(req *http.Request) ([]byte, error) {
	body, err := ac.roundTrip(req)
	retry, ok := ac.renewedRequest(req, err)
	if !ok {
		return body, err
	}
	return ac.roundTrip(retry)
}

// renewedRequest reports whether err is a renewable stale-token rejection and,
// if the renewal succeeds, returns the request to replay.
func (ac *AlpaconClient) renewedRequest(req *http.Request, err error) (*http.Request, bool) {
	if !isStaleCredential(err) {
		return nil, false
	}
	sent, ok := strings.CutPrefix(req.Header.Get("Authorization"), bearerPrefix)
	if !ok {
		// A legacy API key or a service token: no refresh-token grant stands
		// behind it, so a renewal would spend an Auth0 round trip to fail.
		return nil, false
	}
	if !ac.renewAccessToken(sent) {
		return nil, false
	}
	retry, ok := replayableClone(req)
	if !ok {
		return nil, false
	}
	return ac.setHTTPHeader(retry), true
}

// renewAccessToken installs a fresh access token, reporting whether one is now
// in hand. sent is the token the rejected request carried: a different one means
// another request in flight already renewed it, and retrying with that one beats
// spending a second refresh-token grant on the same expiry.
func (ac *AlpaconClient) renewAccessToken(sent string) bool {
	ac.refreshMu.Lock()
	defer ac.refreshMu.Unlock()
	if ac.accessToken() != sent {
		return true
	}
	if err := refreshAccessToken(ac); err != nil {
		// The caller goes on to surface the server's own 401, which says to log
		// in again but not why the renewal behind it failed. Without this line
		// a long wait that dies on a rejected refresh token leaves no trace of
		// the rejection.
		utils.CliDebug("access token renewal failed: %v", err)
		return false
	}
	return true
}

// isStaleCredential reports whether err is the 401 a mid-flight token expiry
// produces. alpacon-server's Auth0 authenticator swallows an expired token and
// returns no user (auth0/auth.py), so the request falls through to
// IsAuthenticatedOr401 and raises DRF's NotAuthenticated—which no branch of the
// server's error_code_handler rewrites, leaving a 401 with a detail and no code.
// Every deliberate 401 (MFA required, IP not allowed, token ACL) carries a code,
// so the empty code slot is what separates a credential this process can renew
// from a refusal it cannot.
func isStaleCredential(err error) bool {
	if utils.HTTPStatusCode(err) != http.StatusUnauthorized {
		return false
	}
	var ae *apiError
	if !errors.As(err, &ae) || !ae.apiPayload {
		// A 401 the server did not write: a gateway refused the request before
		// it arrived. No token this process can obtain changes that answer, so
		// renewing would spend an Auth0 round trip and a config rewrite to be
		// rejected the same way.
		return false
	}
	code, _ := utils.ParseErrorResponse(err)
	return code == ""
}

// replayableClone clones req with a rewound body, reporting false when the body
// cannot be rewound. Such a body carries no GetBody—a streamed upload that is
// not a file—and replaying it would send a truncated one.
func replayableClone(req *http.Request) (*http.Request, bool) {
	if req.GetBody == nil {
		if req.Body != nil {
			return nil, false
		}
		return req.Clone(req.Context()), true
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, false
	}
	clone := req.Clone(req.Context())
	clone.Body = body
	return clone, true
}

func (ac *AlpaconClient) roundTrip(req *http.Request) ([]byte, error) {
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
		return nil, withRetryAfter(withStatus(parseAPIError(respBody), resp.StatusCode), resp.Header)
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

	return ac.sendRequest(req)
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

	resp, err := ac.downloadRoundTrip(req)
	retry, ok := ac.renewedRequest(req, err)
	if !ok {
		return resp, err
	}
	return ac.downloadRoundTrip(retry)
}

// downloadRoundTrip surfaces a 401/403 as an error and leaves the body open on
// every other status for the caller to stream. The status is tagged onto the
// error so the stale-token retry—and utils.HTTPStatusCode—can read it.
func (ac *AlpaconClient) downloadRoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := ac.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, withStatus(checkAuthStatus(resp.StatusCode, body), resp.StatusCode)
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
	ac.refreshMu.Lock()
	defer ac.refreshMu.Unlock()
	return ac.refreshLocked()
}

// accessToken reads the token a request should carry. Every read goes through
// here because sendRequest can replace it between two requests.
func (ac *AlpaconClient) accessToken() string {
	ac.tokenMu.Lock()
	defer ac.tokenMu.Unlock()
	return ac.AccessToken
}

// setAccessToken installs the token every later request carries.
func (ac *AlpaconClient) setAccessToken(token string) {
	ac.tokenMu.Lock()
	defer ac.tokenMu.Unlock()
	ac.AccessToken = token
}

// refreshLocked runs the refresh-token grant and installs the new access token.
// The caller holds refreshMu; auth0.RefreshAccessToken uses the bare HTTP
// client, so it cannot re-enter sendRequest and deadlock on it.
func (ac *AlpaconClient) refreshLocked() error {
	cfg, err := config.LoadConfig()
	if err != nil {
		return err
	}
	if cfg.RefreshToken == "" {
		return errors.New("no refresh token stored; run 'alpacon login' to authenticate again")
	}
	tokenRes, err := auth0.RefreshAccessToken(ac.BaseURL, ac.HTTPClient, cfg.RefreshToken)
	if err != nil {
		return err
	}
	ac.setAccessToken(tokenRes.AccessToken)
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

func (e *apiError) HTTPStatusCode() int {
	return e.statusCode
}

func newAPIError(message, code, source string) error {
	return &apiError{message: message, code: code, source: source}
}

// withStatus tags err with its HTTP status so callers can tell 404 from 401,
// wrapping non-*apiError errors so the status survives the error chain.
func withStatus(err error, statusCode int) error {
	if err == nil {
		return nil
	}
	var ae *apiError
	if errors.As(err, &ae) {
		ae.statusCode = statusCode
		return err
	}
	return &statusError{err: err, statusCode: statusCode}
}

// withRetryAfter tags err with the Retry-After delay. Only delta-seconds is parsed—
// that is what DRF throttling sends, and misreading an HTTP-date would stall a poll.
func withRetryAfter(err error, header http.Header) error {
	if err == nil {
		return nil
	}
	seconds, convErr := strconv.Atoi(strings.TrimSpace(header.Get("Retry-After")))
	// The upper bound is what a time.Duration can hold: past it the multiplication
	// below wraps, and a wrapped delay is worse than no hint at all.
	if convErr != nil || seconds <= 0 || int64(seconds) > math.MaxInt64/int64(time.Second) {
		return err
	}
	return &retryAfterError{err: err, retryAfter: time.Duration(seconds) * time.Second}
}

// parseAPIError extracts a human-readable error message from a JSON API error response.
// Handles common formats: {"detail": "..."}, {"field": ["error", ...]}, {"non_field_errors": ["..."]}
func parseAPIError(body []byte) error {
	message, code, source, _ := parseAPIErrorPayload(body)
	return newAPIError(message, code, source)
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
