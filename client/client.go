package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api/auth0"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/aiagent"
	"github.com/alpacax/alpacon-cli/pkg/httpclient"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	currentUserURL = "/api/iam/users/-/"

	// bearerPrefix is the scheme setHTTPHeader signs an access token with. The
	// stale-token retry reads it back off the request to learn which token the
	// server rejected.
	bearerPrefix = "Bearer "

	// ClientCapabilitiesHeader carries the optional behaviors a WebSocket
	// connection supports, as a comma-separated list.
	ClientCapabilitiesHeader = "X-Alpacon-Client-Capabilities"

	// CapabilityWebsocketReconnect says the client re-dials a new channel and
	// resumes the session when the connection drops, so closing the connection
	// for a transient reason does not take the session with it.
	CapabilityWebsocketReconnect = "websocket-reconnect"

	// serverGateTokenScope is the "gate" value the server sends on a
	// token-scope refusal (api_token_scope_missing, api_token_scope_action_unresolved).
	// It is the only gate whose "missing" is a scope string; every other gate's
	// "missing" names a permission or role, so authStatusMessage's fallback
	// message only uses "scope" wording when the gate is this one.
	serverGateTokenScope = "token_scope"

	// reauthenticateMessage covers a detail-less 401 that is uncoded or coded
	// auth_token_missing/auth_authentication_failed.
	reauthenticateMessage = "authentication failed: please run 'alpacon login' again"

	// signInMFAMessage covers a 401 coded auth_token_mfa_missing: a refresh keeps
	// the token's MFA state, so only a new login, which runs MFA, can lift it.
	signInMFAMessage = "multi-factor authentication required at sign-in: please run 'alpacon login' again"

	// gatePlan is the "gate" value the server sends on every 402 plan
	// refusal, limit or feature lock alike.
	gatePlan = "plan"

	// axisServer is the one axis whose remedy carries an extra hint: a host that
	// hit the server cap may be a re-registration of one deleted-but-not-freed
	// entry rather than a genuinely new one.
	axisServer = "server"

	talkToUsURL             = "https://www.alpacax.com/alpacon/pricing"
	consoleBillingURLFormat = "https://alpacon.io/%s/settings/billing"
	selfHostedBillingWords  = "Settings → Billing in your Alpacon console"

	countCapRemedy           = "Remove one you no longer use, or upgrade the plan."
	serverAxisReregisterHint = "If this host was registered before, delete the old server entry first, then retry."
	featureLockSentence      = "This action needs a higher plan."
)

// refreshAccessToken is a test seam so a unit test can drive the stale-token
// retry without real Auth0 I/O.
var refreshAccessToken = (*AlpaconClient).refreshLocked

func NewAlpaconAPIClient() (*AlpaconClient, error) {
	validConfig, err := config.LoadConfig()
	if err != nil {
		return nil, fmt.Errorf("configuration file not found or invalid: %w. Please run 'alpacon login' to configure your connection", err)
	}

	httpClient := httpclient.New(validConfig.Insecure)

	client := &AlpaconClient{
		HTTPClient:    httpClient,
		BaseURL:       validConfig.WorkspaceURL,
		WorkspaceName: validConfig.WorkspaceIdentity(),
		Token:         validConfig.Token,
		UserAgent:     utils.GetUserAgent(),
		Insecure:      validConfig.Insecure,
	}
	client.SetAccessToken(validConfig.AccessToken)

	if isAccessTokenExpired(validConfig) {
		if validConfig.RefreshToken == "" {
			return nil, fmt.Errorf("authentication session expired. Please run 'alpacon login' again")
		}
		spinner := utils.NewSpinner("Refreshing access token...")
		spinner.Start()
		tokenRes, err := auth0.RefreshAccessToken(validConfig.WorkspaceURL, httpClient, validConfig.RefreshToken)
		spinner.Stop()
		if err != nil {
			if auth0.IsRefreshTokenRejected(err) {
				return nil, fmt.Errorf("authentication session expired. Please run 'alpacon login' again: %w", err)
			}
			return nil, fmt.Errorf("could not complete access token refresh. Please retry: %w", err)
		}

		client.SetAccessToken(tokenRes.AccessToken)
	}

	return client, nil
}

func (ac *AlpaconClient) LoadCurrentUser() error {
	ac.loadOnce.Do(func() {
		body, err := ac.SendGetRequest(currentUserURL)
		if err != nil {
			ac.loadErr = err
			return
		}
		var resp CurrentUserResponse
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

// SetWebsocketHeader builds the header for a WebSocket dial. gorilla sends no
// User-Agent of its own, so a client assembled without one—every client outside
// NewAlpaconAPIClient—would dial anonymously; fall back to the same string the
// HTTP requests carry. The handshake declares a detected AI agent the same way
// every HTTP request does.
func (ac *AlpaconClient) SetWebsocketHeader() http.Header {
	userAgent := ac.UserAgent
	if userAgent == "" {
		userAgent = utils.GetUserAgent()
	}

	headers := http.Header{}
	headers.Set("Origin", ac.BaseURL)
	headers.Set("User-Agent", userAgent)
	aiagent.FromEnvironment().SetHeaders(headers)

	return headers
}

// SetWebsocketHeaderWithCapabilities is SetWebsocketHeader plus the capabilities
// this connection supports, so the server can tell a client that re-dials a
// dropped connection from one that ends the session on it. Only a caller that
// actually implements a capability may advertise it; a server that does not read
// the header is unaffected either way.
func (ac *AlpaconClient) SetWebsocketHeaderWithCapabilities(capabilities ...string) http.Header {
	headers := ac.SetWebsocketHeader()
	if len(capabilities) > 0 {
		headers.Set(ClientCapabilitiesHeader, strings.Join(capabilities, ", "))
	}

	return headers
}

// setHTTPHeader signs req and declares a detected AI agent. Every request the
// client sends passes through here, the stale-token replay included.
func (ac *AlpaconClient) setHTTPHeader(req *http.Request) *http.Request {
	req.Header.Set("User-Agent", ac.UserAgent)
	aiagent.FromEnvironment().SetHeaders(req.Header)
	if accessToken := ac.AccessToken(); accessToken != "" {
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
// process-wide credential is stale, not the request: the server rejects a
// stale token before the request is handled, so the first attempt changed
// nothing and the replay is the same request rather than a second one. A
// client built at startup and held for a half-hour approval wait would
// otherwise never re-enter the refresh that NewAlpaconAPIClient runs once.
func (ac *AlpaconClient) sendRequest(req *http.Request) ([]byte, error) {
	body, err := ac.roundTrip(req)
	retry, ok := ac.renewedRequest(req, err)
	if !ok {
		return body, err
	}
	return ac.roundTrip(retry)
}

// sendRequestWithStatus is sendRequest plus the response's HTTP status code; see roundTripWithStatus.
func (ac *AlpaconClient) sendRequestWithStatus(req *http.Request) ([]byte, int, error) {
	body, status, err := ac.roundTripWithStatus(req)
	retry, ok := ac.renewedRequest(req, err)
	if !ok {
		return body, status, err
	}
	return ac.roundTripWithStatus(retry)
}

// renewedRequest reports whether err is a renewable stale-token rejection and,
// if the renewal succeeds, returns the request to replay.
func (ac *AlpaconClient) renewedRequest(req *http.Request, err error) (*http.Request, bool) {
	if ac.noRenewal || !isStaleCredential(err) {
		return nil, false
	}
	sent, ok := strings.CutPrefix(req.Header.Get("Authorization"), bearerPrefix)
	if !ok {
		// A legacy API key or a service token: no refresh-token grant stands
		// behind it, so a renewal would spend an Auth0 round trip to fail.
		return nil, false
	}
	if !ac.renewAccessTokenWithin(req.Context(), sent) {
		return nil, false
	}
	retry, ok := replayableClone(req)
	if !ok {
		return nil, false
	}
	return ac.setHTTPHeader(retry), true
}

// renewAccessTokenWithin is renewAccessToken bounded by ctx: once ctx ends it reports false, and the
// renewal finishes in the background, where its token lands and a save warning may print after the return.
func (ac *AlpaconClient) renewAccessTokenWithin(ctx context.Context, sent string) bool {
	if ctx.Done() == nil {
		return ac.renewAccessToken(sent)
	}
	renewed := make(chan bool, 1)
	go func() { renewed <- ac.renewAccessToken(sent) }()
	select {
	case ok := <-renewed:
		return ok
	case <-ctx.Done():
		return false
	}
}

// renewAccessToken installs a fresh access token, reporting whether one is now
// in hand. sent is the token the rejected request carried: a different one means
// another request in flight already renewed it, and retrying with that one beats
// spending a second refresh-token grant on the same expiry. A failed grant
// leaves the token as it was, so the sent check collapses nothing—every other
// in-flight request that took the same 401 runs its own grant. The in-flight
// count bounds that and each request still retries at most once; a caller that
// ever fans requests out widely enough for the bound to hurt should remember
// the failure for the life of the client instead.
func (ac *AlpaconClient) renewAccessToken(sent string) bool {
	ac.refreshMu.Lock()
	defer ac.refreshMu.Unlock()
	if ac.AccessToken() != sent {
		return true
	}
	if err := refreshAccessToken(ac); err != nil {
		// The caller goes on to surface the server's own 401, which says to log
		// in again but not why the renewal behind it failed. Without this line
		// a long wait that dies on a rejected refresh token leaves no trace of
		// the rejection.
		utils.CliDebug("access token renewal failed: %v", utils.RedactURLError(err))
		return false
	}
	return true
}

// isStaleCredential reports whether err is a 401 a fresh access token could
// plausibly move. A code-less 401, or one coded auth_token_missing, may be a
// stale access token. Other Auth0 bearer rejections—a workspace mismatch, MFA,
// an uninvited user—also land there and cost one renewal and one replay before
// surfacing the same error. So that 401 is not proof of expiry—it is the only
// one worth spending one retry on, because a coded refusal (MFA required, IP
// not allowed, token ACL, auth_authentication_failed, auth_token_mfa_missing)
// names what it wants and a new token is not it.
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
	return code == "" || code == utils.AuthTokenMissing
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
	body, _, err := ac.roundTripWithStatus(req)
	return body, err
}

// roundTripWithStatus is roundTrip plus the response's HTTP status code on
// success, for the rare endpoint whose body shape is the same on two
// different success codes and a caller must tell them apart (e.g.
// work-session extend's 200-vs-202 contract). On failure the status is also
// returned for convenience, though it is already recoverable from err via
// utils.HTTPStatusCode.
func (ac *AlpaconClient) roundTripWithStatus(req *http.Request) ([]byte, int, error) {
	resp, err := ac.HTTPClient.Do(req)
	if err != nil {
		return nil, redirectRefusedStatus(resp), utils.WithRedirectStatus(utils.RedactURLError(err), resp)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := readJSONResponse(resp)
	if err != nil {
		return nil, resp.StatusCode, err
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		apiErr := withRetryAfter(withStatus(parseAPIError(respBody, resp.StatusCode), resp.StatusCode), resp.Header)
		if resp.StatusCode == http.StatusPaymentRequired {
			apiErr = ac.rewritePlanLimitMessage(apiErr)
		}
		return nil, resp.StatusCode, apiErr
	}

	return respBody, resp.StatusCode, nil
}

// Get Request to Alpacon Server
func (ac *AlpaconClient) SendGetRequest(url string) ([]byte, error) {
	return ac.SendGetRequestWithContext(context.Background(), url)
}

// SendGetRequestWithContext is SendGetRequest bounded by ctx, a token renewal it
// runs included: a renewal that outlasts ctx leaves the request's 401 as its result.
func (ac *AlpaconClient) SendGetRequestWithContext(ctx context.Context, url string) ([]byte, error) {
	req, err := ac.createRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return ac.sendRequest(req.WithContext(ctx))
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

// SendPostRequestWithStatus is SendPostRequest plus the response's HTTP status
// code on success. Use it only where the status code itself carries meaning
// the body cannot be trusted to (e.g. work-session extend's 200-vs-202
// contract, where the body may still carry a field from before the status
// existed to disambiguate it)—every other caller wants SendPostRequest.
func (ac *AlpaconClient) SendPostRequestWithStatus(url string, body any) ([]byte, int, error) {
	jsonValue, err := json.Marshal(body)
	if err != nil {
		return nil, 0, err
	}

	req, err := ac.createRequest(http.MethodPost, url, bytes.NewBuffer(jsonValue))
	if err != nil {
		return nil, 0, err
	}
	return ac.sendRequestWithStatus(req)
}

func (ac *AlpaconClient) SendDeleteRequest(url string) ([]byte, error) {
	return ac.SendDeleteRequestWithContext(context.Background(), url)
}

// SendDeleteRequestWithContext is SendDeleteRequest bounded by ctx, a token renewal it
// runs included: a renewal that outlasts ctx leaves the request's 401 as its result.
func (ac *AlpaconClient) SendDeleteRequestWithContext(ctx context.Context, url string) ([]byte, error) {
	req, err := ac.createRequest(http.MethodDelete, url, nil)
	if err != nil {
		return nil, err
	}
	return ac.sendRequest(req.WithContext(ctx))
}

// SendDeleteRequestWithBody sends a DELETE carrying a JSON body: an endpoint wanting a
// justification takes it there rather than in a query parameter, so it stays out of access
// logs, proxy logs and shell history. createRequest sets the content type only for methods
// that always carry a body, so this sets it here.
//
// A DELETE entity-body is legal but unusual: an intermediary that strips one still
// leaves a 204 and an audit row with an empty reason, undetectable from here.
func (ac *AlpaconClient) SendDeleteRequestWithBody(url string, body any) ([]byte, error) {
	jsonValue, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := ac.createRequest(http.MethodDelete, url, bytes.NewBuffer(jsonValue))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

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
		// A download URL may be presigned, and a transport error prints it whole.
		return nil, utils.WithRedirectStatus(utils.RedactURLError(err), resp)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, withStatus(checkAuthStatus(resp.StatusCode, body), resp.StatusCode)
	}
	// A 402, 405 or 429 is a coded refusal too: the shared parser keeps its
	// code and hint, where the caller would only have the raw body to print.
	if utils.IsRefusalStatus(resp.StatusCode) {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, withRetryAfter(withStatus(parseAPIError(body, resp.StatusCode), resp.StatusCode), resp.Header)
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

// AccessToken returns the token a request should carry. It locks because
// sendRequest can swap the token mid-flight.
func (ac *AlpaconClient) AccessToken() string {
	ac.tokenMu.Lock()
	defer ac.tokenMu.Unlock()
	return ac.accessToken
}

// IsBearerAuth reports whether requests carry an Auth0 bearer token rather than a
// legacy API key. Some endpoints refuse the API key outright, and their refusal arrives
// with no error code, so a caller rewriting it has to know which credential it sent.
func (ac *AlpaconClient) IsBearerAuth() bool {
	return ac.AccessToken() != ""
}

// SetAccessToken installs the token every later request carries. It is exported
// so tests in other packages can build an authenticated client without writing
// the field directly and skipping tokenMu.
func (ac *AlpaconClient) SetAccessToken(token string) {
	ac.tokenMu.Lock()
	defer ac.tokenMu.Unlock()
	ac.accessToken = token
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
	ac.SetAccessToken(tokenRes.AccessToken)
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
