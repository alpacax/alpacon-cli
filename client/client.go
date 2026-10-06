package client

import (
	"bytes"
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

// legacyPlanLimitAxis maps a 402's "code" to its axis for a server that
// predates the "gate"/"axis" envelope. Deliberately excludes
// workspace_free_limit_exceeded: that gate-less 402 can also be a
// Free-workspace refusal, which this mapping must not repaint as a plan
// limit.
var legacyPlanLimitAxis = map[string]string{
	"server_limit_exceeded":      "server",
	"user_limit_exceeded":        "user",
	"application_limit_exceeded": "application",
	"websh_limit_exceeded":       "websh",
	"webftp_limit_exceeded":      "webftp",
	"websh_share_limit_exceeded": "websh-share",
}

// axisDisplayNames renders an axis for a human reader.
var axisDisplayNames = map[string]string{
	"server":      "servers",
	"user":        "users (pending invitations count)",
	"application": "applications",
	"websh":       "Websh hours this month",
	"webftp":      "WebFTP transfer volume this month",
	"websh-share": "Websh session sharing",
	"workspace":   "Free workspaces",
}

// refreshAccessToken is a test seam so a unit test can drive the stale-token
// retry without real Auth0 I/O.
var refreshAccessToken = (*AlpaconClient).refreshLocked

type apiError struct {
	message string
	code    string
	source  string
	// gate and missing carry the optional "gate"/"missing" fields a coded
	// 402/403/405/429 may send beside "code": the server states which gate
	// refused the request and what it was missing, but sends no human "detail"
	// on these routes, so a caller such as cmd/iam's RBAC guidance table reads
	// these instead of trying to parse one out of nothing. missing is normalized
	// to a slice regardless of whether the server sent one scope string or a list.
	gate    string
	missing []string
	// axis and next carry the optional 402 plan-limit fields: axis names which
	// limit refused the request, and next is a self-relative entitlements-read
	// path a member-scoped caller can read for the numbers the 402 body never
	// carries. planLimitMessage reads axis to build the user-facing text; next
	// is carried for a future programmatic reader, not this CLI's plain-text
	// rendering.
	axis string
	next string
	// retryAfter is the Retry-After header's parsed delay, set by
	// withRetryAfter alongside the *retryAfterError wrapper it returns, so
	// planLimitMessage can build "resets in N days" without re-parsing the
	// header itself.
	retryAfter time.Duration
	statusCode int
	// apiPayload records that the body was a JSON object—the shape every
	// server error response has. It is a filter, not a provenance flag:
	// only the negative holds, since anything standing in front of the server
	// can emit a JSON object too. The stale-token retry reads it to drop
	// gateway refusals, and nothing may read it as proof of who wrote the body.
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

	httpClient := httpclient.New(validConfig.Insecure)

	client := &AlpaconClient{
		HTTPClient:    httpClient,
		BaseURL:       validConfig.WorkspaceURL,
		WorkspaceName: validConfig.WorkspaceIdentity(),
		Token:         validConfig.Token,
		UserAgent:     utils.GetUserAgent(),
	}
	client.SetAccessToken(validConfig.AccessToken)

	if isAccessTokenExpired(validConfig) {
		spinner := utils.NewSpinner("Refreshing access token...")
		spinner.Start()
		tokenRes, err := auth0.RefreshAccessToken(validConfig.WorkspaceURL, httpClient, validConfig.RefreshToken)
		spinner.Stop()
		if err != nil {
			return nil, fmt.Errorf("failed to refresh access token: %v. Your session may have expired completely. Please run 'alpacon login' to authenticate again", err)
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

// checkAuthStatus prefers the server-provided reason and only suggests
// re-login for a bare authentication failure—never for a coded condition such
// as MFA-required or a policy denial, which re-login does not resolve. The
// error code is always preserved on the returned error so downstream handlers
// (MFA flow, WorkSession gate) can still route on it.
func checkAuthStatus(statusCode int, body []byte) error {
	if statusCode != http.StatusUnauthorized && statusCode != http.StatusForbidden {
		return nil
	}
	detail, code, source, gate, missing, hasDetail := parseAuthStatusErrorPayload(body, statusCode)
	return &apiError{
		message:    authStatusMessage(statusCode, code, detail, hasDetail, gate, missing),
		code:       code,
		source:     source,
		gate:       gate,
		missing:    missing,
		apiPayload: isJSONObject(body),
	}
}

// isJSONObject reports whether body is a JSON object. The server renders
// every error as one, so anything else on a 401—an HTML page, a bare string,
// nothing at all—came from a proxy, a WAF or an mTLS gate ahead of it. Only
// that direction holds: a JSON object clears the test whoever wrote it.
func isJSONObject(body []byte) bool {
	var parsed map[string]any
	return json.Unmarshal(body, &parsed) == nil && parsed != nil
}

// authStatusMessage renders the user-facing message for a 401/403. It prefers
// the server's human detail; absent that, a known structured code maps to a
// clear message. Re-login is suggested only for a code-less 401 or one coded
// auth_token_missing/auth_authentication_failed—an authenticated user who
// merely needs MFA, or who hit a policy denial, must not be told to log in again.
func authStatusMessage(statusCode int, code, detail string, hasDetail bool, gate string, missing []string) string {
	if hasDetail {
		if statusCode == http.StatusUnauthorized && code == "" {
			return fmt.Sprintf("%s (run 'alpacon login' if your session has expired)", detail)
		}
		return detail
	}
	if msg, ok := authStatusCodeMessage(statusCode, code); ok {
		return msg
	}
	if statusCode == http.StatusUnauthorized {
		if code == "" {
			return reauthenticateMessage
		}
		// A coded 401 is a deliberate server decision, not a stale token; do not
		// mislabel it as an authentication failure or suggest re-login.
		// Deliberately information-light: do not interpolate the raw code into the
		// message—callers read it via ErrorCode(), and embedding it would break the
		// no-raw-code contract that TestSendRequest_403CodeWithoutDetailKeepsCodeSource guards.
		return "request denied by server"
	}
	// A coded 403 with no detail but a stated "missing": name it rather than fall
	// back to the generic line. A caller with more specific guidance for its own
	// code (cmd/iam's RBAC gates) overrides this; this is the floor for every
	// other caller that just surfaces the error as-is. "missing" is not always a
	// scope—the role gate's "missing" names a permission/role, not a
	// "scope:..." string—so only the token-scope gate gets scope wording; every
	// other gate (including an absent one, from a server this CLI does not yet
	// know) gets gate-neutral wording instead.
	if len(missing) > 0 {
		if gate == serverGateTokenScope {
			return fmt.Sprintf("permission denied: missing scope %s", strings.Join(missing, ", "))
		}
		return fmt.Sprintf("permission denied: missing %s", strings.Join(missing, ", "))
	}
	return "permission denied: you do not have the required privileges for this action"
}

// authStatusCodeMessage maps structured server codes that arrive on a 401/403
// without a human detail to a clear, actionable message. The login hint for
// auth_token_missing/auth_authentication_failed is 401-only; a 403 carrying
// either falls through to the generic permission-denied floor.
func authStatusCodeMessage(statusCode int, code string) (string, bool) {
	switch code {
	case utils.AuthMFARequired:
		return "multi-factor authentication required—complete MFA to continue", true
	case utils.APITokenACLNotAllowed:
		return "denied by token access control—this token may not perform that action; review its rules with 'alpacon token acl'; an interactive Websh terminal or a tunnel needs browser login, which no rule can grant", true
	case utils.WorkspaceSudoWithMFAStepUpUnavailable:
		return "this setting cannot be changed on this server because MFA sign-in is not available; a server administrator has to change it", true
	case utils.SudoVerifyCredentialCannotProveMFA:
		return "this credential cannot complete sudo MFA—sign in with 'alpacon login' to complete sudo MFA", true
	case utils.AuthTokenMissing, utils.AuthAuthenticationFailed:
		if statusCode == http.StatusUnauthorized {
			return reauthenticateMessage, true
		}
	}
	return "", false
}

// parseAuthStatusErrorPayload returns ok=true with a "detail" message or with
// rendered field_errors messages; code/source/gate/missing are returned
// regardless so the WorkSession gate can route to exit 3 and a coded refusal
// without either still carries what it can.
func parseAuthStatusErrorPayload(body []byte, statusCode int) (message string, code string, source string, gate string, missing []string, ok bool) {
	// axis/next ride only on the 402 plan-limit envelope (client.go's
	// planLimitMessage), never on a 401/403; this path ignores them, and
	// isEnvelopeOnly's own statusCode gate keeps a same-named validation field
	// from being swallowed regardless.
	message, code, source, gate, _, _, missing, ok = parseAPIErrorPayload(body, statusCode)
	if !ok || message == "" {
		return "", code, source, gate, missing, false
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", code, source, gate, missing, false
	}
	if stringField(parsed, "detail") != "" {
		return message, code, source, gate, missing, true
	}
	if len(fieldErrorMessages(parsed["field_errors"])) > 0 {
		return message, code, source, gate, missing, true
	}
	return "", code, source, gate, missing, false
}

// SetWebsocketHeader builds the header for a WebSocket dial. gorilla sends no
// User-Agent of its own, so a client assembled without one—every client outside
// NewAlpaconAPIClient—would dial anonymously; fall back to the same string the
// HTTP requests carry.
func (ac *AlpaconClient) SetWebsocketHeader() http.Header {
	userAgent := ac.UserAgent
	if userAgent == "" {
		userAgent = utils.GetUserAgent()
	}

	headers := http.Header{}
	headers.Set("Origin", ac.BaseURL)
	headers.Set("User-Agent", userAgent)

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

func (ac *AlpaconClient) setHTTPHeader(req *http.Request) *http.Request {
	req.Header.Set("User-Agent", ac.UserAgent)
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
		utils.CliDebug("access token renewal failed: %v", err)
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
// not allowed, token ACL, auth_authentication_failed) names what it wants and
// a new token is not it.
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
		return nil, 0, err
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

// rewritePlanLimitMessage replaces err's message with planLimitMessage's
// rendering when the 402 it carries classifies as a plan limit or a
// feature lock (client classification rules 1–3); rule 4 (any other 402)
// leaves the generic message parseAPIError already built untouched.
func (ac *AlpaconClient) rewritePlanLimitMessage(err error) error {
	var ae *apiError
	if !errors.As(err, &ae) {
		return err
	}
	if message, ok := planLimitMessage(ae.axis, ae.code, ae.gate, ae.retryAfter, ac.BaseURL); ok {
		ae.message = message
	}
	return err
}

// planLimitMessage implements the client classification and the CLI/MCP/alpamon
// message template for a 402 from the server:
//
//  1. gate=="plan" with axis present: a plan limit on axis.
//  2. gate absent and code is one of the six legacy *_limit_exceeded codes: a
//     plan limit, axis from legacyPlanLimitAxis (an older server, or an agent
//     talking to one, that predates "gate"/"axis").
//  3. gate=="plan" with no axis: a feature lock, not a limit.
//  4. Anything else: not a plan-limit response at all; ok is false and the
//     caller leaves today's message alone.
func planLimitMessage(axis, code, gate string, retryAfter time.Duration, workspaceURL string) (string, bool) {
	switch {
	case gate == gatePlan && axis != "":
		// rule 1: axis already carried by the modern envelope.
	case gate == "":
		mapped, known := legacyPlanLimitAxis[code]
		if !known {
			return "", false // rule 4
		}
		axis = mapped // rule 2
	case gate == gatePlan:
		// rule 3: a feature lock—no axis to name a limit on.
		return fmt.Sprintf("%s Upgrade: %s. Talk to us: %s", featureLockSentence, billingLocation(workspaceURL), talkToUsURL), true
	default:
		return "", false // rule 4: some other gate, e.g. role or token_scope
	}

	message := fmt.Sprintf("plan limit reached: %s. %s Upgrade: %s. Talk to us: %s",
		axisDisplayName(axis), planLimitRemedy(axis, retryAfter), billingLocation(workspaceURL), talkToUsURL)
	return message, true
}

// axisDisplayName renders axis for a human reader, falling back to the raw
// value for an axis this CLI does not yet know (a future server addition).
func axisDisplayName(axis string) string {
	if name, ok := axisDisplayNames[axis]; ok {
		return name
	}
	return axis
}

// planLimitRemedy picks the count-cap or monthly-reset remedy sentence:
// retryAfter is only ever set on a monthly axis (websh, webftp, websh-share)
// and only when the limit is above zero, so its presence alone decides
// which sentence applies. axisServer gets an extra hint no other axis needs:
// a host cap is the one limit a stale, undeleted entry can trip by accident.
func planLimitRemedy(axis string, retryAfter time.Duration) string {
	remedy := countCapRemedy
	if retryAfter > 0 {
		remedy = monthlyRemedy(retryAfter)
	}
	if axis == axisServer {
		remedy += " " + serverAxisReregisterHint
	}
	return remedy
}

// monthlyRemedy renders "resets in N days" from the Retry-After delay,
// rounded to the nearest day and floored at one so a delay under twelve hours
// never reads as "0 days".
func monthlyRemedy(retryAfter time.Duration) string {
	days := int(retryAfter.Round(24*time.Hour) / (24 * time.Hour))
	if days < 1 {
		days = 1
	}
	return fmt.Sprintf("Resets in %d days.", days)
}

// billingLocation is the console billing page for a workspace whose host is
// <label>.<region>.alpacon.io, or the words pointing there for any other host
// (self-hosted): a public repo never prints an internal or unverified URL.
func billingLocation(workspaceURL string) string {
	label, ok := consoleLabel(workspaceURL)
	if !ok {
		return selfHostedBillingWords
	}
	return fmt.Sprintf(consoleBillingURLFormat, label)
}

// consoleLabel extracts the workspace label from a canonical Alpacon Cloud
// host. It mirrors cmd/login.go's isCloudWorkspaceURL—the CLI's other
// definition of "this is Alpacon Cloud"—rather than sharing it: client cannot
// import cmd. The URL must round-trip, case-insensitively, to exactly
// https://<label>.<region>.alpacon.io; a different scheme, a port, a path, or
// a non-4-label host all fail this, so a self-hosted endpoint whose hostname
// merely resembles the pattern (or a cloud host spelled in another case)
// never gets an unverified—or missing—cloud billing link.
func consoleLabel(workspaceURL string) (string, bool) {
	raw := strings.TrimSpace(workspaceURL)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	parts := strings.Split(parsed.Hostname(), ".")
	if len(parts) != 4 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	if !strings.EqualFold(strings.Join(parts[2:], "."), "alpacon.io") {
		return "", false
	}
	canonical := fmt.Sprintf("https://%s.%s.alpacon.io", parts[0], parts[1])
	if !strings.EqualFold(strings.TrimSuffix(raw, "/"), canonical) {
		return "", false
	}
	return parts[0], true
}

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
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return &RawResponse{Status: resp.Status, StatusCode: resp.StatusCode, Proto: resp.Proto, Header: resp.Header.Clone(), Body: respBody}, nil
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
	req, err := ac.createRequest(http.MethodDelete, url, nil)
	if err != nil {
		return nil, err
	}
	return ac.sendRequest(req)
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

func (e *apiError) Error() string {
	return e.message
}

func (e *apiError) ErrorCode() string {
	return e.code
}

func (e *apiError) ErrorSource() string {
	return e.source
}

func (e *apiError) ErrorGate() string {
	return e.gate
}

func (e *apiError) ErrorMissing() []string {
	return e.missing
}

func (e *apiError) HTTPStatusCode() int {
	return e.statusCode
}

func newAPIError(message, code, source, gate, axis, next string, missing []string) error {
	return &apiError{message: message, code: code, source: source, gate: gate, axis: axis, next: next, missing: missing}
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
// that is what a throttled response sends, and misreading an HTTP-date would
// stall a poll.
// When err is (or wraps) an *apiError, the delay is also stored there directly so
// planLimitMessage can read it without re-parsing the header or unwrapping the
// *retryAfterError this still returns for every other caller (the exec/websh poll
// loops read RetryAfter() off that wrapper).
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
	retryAfter := time.Duration(seconds) * time.Second
	var ae *apiError
	if errors.As(err, &ae) {
		ae.retryAfter = retryAfter
	}
	return &retryAfterError{err: err, retryAfter: retryAfter}
}

// parseAPIError extracts a human-readable error message from a JSON API error response.
// Handles common formats: {"detail": "..."}, {"field": ["error", ...]}, {"non_field_errors": ["..."]},
// {"field_errors": {"path": [{"code", "message"}, ...]}}
// statusCode gates whether "axis"/"next" are read as the 402 plan-limit
// envelope (isEnvelopeOnly) rather than as ordinary validation fields.
func parseAPIError(body []byte, statusCode int) error {
	message, code, source, gate, axis, next, missing, _ := parseAPIErrorPayload(body, statusCode)
	return newAPIError(message, code, source, gate, axis, next, missing)
}

func parseAPIErrorPayload(body []byte, statusCode int) (message string, code string, source string, gate string, axis string, next string, missing []string, ok bool) {
	raw := string(body)

	if strings.TrimSpace(raw) == "" {
		return "server returned an empty error response", "", "", "", "", "", nil, false
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		// Not valid JSON (e.g., HTML error page) — return truncated
		return truncateBody(raw), "", "", "", "", "", nil, false
	}

	code = stringField(parsed, "code")
	source = stringField(parsed, "source")
	gate, missing = parseGateAndMissing(parsed)
	axis = stringField(parsed, "axis")
	next = stringField(parsed, "next")

	// Case 1: {"detail": "..."}
	if detail := strings.TrimSpace(stringField(parsed, "detail")); detail != "" {
		return detail, code, source, gate, axis, next, missing, true
	}

	// A refusal envelope carries no field errors; gate/axis/next/missing are not validation messages.
	if code != "" && isEnvelopeOnly(parsed, statusCode) {
		return codeOnlyMessage(code), code, source, gate, axis, next, missing, true
	}

	if messages := fieldErrorMessages(parsed["field_errors"]); len(messages) > 0 {
		return strings.Join(messages, "; "), code, source, gate, axis, next, missing, true
	}

	// Case 2: field validation errors {"field": ["msg1", ...]}. A "code" whose
	// value is the envelope's string code stays out; a list "code" field is a
	// real request field and is rendered like any other. Keys are sorted
	// for deterministic output.
	fields := make([]string, 0, len(parsed))
	for field := range parsed {
		if field == "field_errors" {
			continue
		}
		if field == "code" {
			if _, isString := parsed[field].(string); isString {
				continue
			}
		}
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
		return strings.Join(messages, "; "), code, source, gate, axis, next, missing, true
	}

	if code != "" {
		return codeOnlyMessage(code), code, source, gate, axis, next, missing, true
	}

	// Fallback: return truncated raw body
	return truncateBody(raw), code, source, gate, axis, next, missing, true
}

// isEnvelopeOnly reports whether parsed holds only envelope-shaped values: a
// string for "code"/"source"/"gate"/"detail", "axis"/"next" the same but only
// on a 402 (the plan-limit fields—on any other status they are not part of
// the envelope, so a validation response that happens to have a field named
// "axis" or "next" still renders as a field error rather than being swallowed
// into a code-only message), and for
// "missing" either a single scope string or a list of them.
func isEnvelopeOnly(parsed map[string]any, statusCode int) bool {
	for key, value := range parsed {
		switch key {
		case "code", "source", "gate", "detail":
			if _, ok := value.(string); !ok {
				return false
			}
		case "axis", "next":
			if statusCode != http.StatusPaymentRequired {
				return false
			}
			if _, ok := value.(string); !ok {
				return false
			}
		case "missing":
			if _, ok := value.(string); ok {
				continue
			}
			if !isStringList(value) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// isStringList reports whether v is a JSON array of strings, "missing"'s
// list shape in the envelope.
func isStringList(v any) bool {
	list, ok := v.([]any)
	if !ok {
		return false
	}
	for _, item := range list {
		if _, ok := item.(string); !ok {
			return false
		}
	}
	return true
}

// fieldErrorMessages renders a "field_errors" object—{"path": [{"code",
// "message"}, ...]}—into sorted "path: message" strings, "non_field_errors"
// bare. A malformed entry contributes nothing.
func fieldErrorMessages(raw any) []string {
	fieldErrors, ok := raw.(map[string]any)
	if !ok {
		return nil
	}

	paths := make([]string, 0, len(fieldErrors))
	for path := range fieldErrors {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	var messages []string
	for _, path := range paths {
		entries, ok := fieldErrors[path].([]any)
		if !ok {
			continue
		}
		for _, entry := range entries {
			fields, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			message := stringField(fields, "message")
			if message == "" {
				continue
			}
			if path == "non_field_errors" {
				messages = append(messages, message)
			} else {
				messages = append(messages, fmt.Sprintf("%s: %s", path, message))
			}
		}
	}
	return messages
}

// codeOnlyMessage renders a code sent with no detail or field errors.
// It serves every status, so it carries no denial-specific wording.
func codeOnlyMessage(code string) string {
	switch code {
	case utils.APINotFound:
		return "the requested resource was not found"
	case utils.APIRateLimited:
		return "too many requests—please try again later"
	case utils.APINotAcceptable:
		return "the server cannot produce a response in the format requested"
	case utils.APIUnsupportedMediaType:
		return "the server does not support the request's content type"
	case utils.AuthVerificationUnavailable:
		return "the server could not verify your credential right now—it was not rejected, so try again in a moment"
	case utils.APICursorExpired:
		return "the search snapshot this page was read from expired—read the listing again from the start"
	case utils.APIInvalidCursor:
		return "the server would not take this page's cursor—read the listing again from the start"
	case utils.APISearchUnavailable:
		return "the search backend would not start this read right now—try again in a moment"
	default:
		return fmt.Sprintf("request failed (code: %s)", code)
	}
}

// parseGateAndMissing extracts the optional "gate" and "missing" fields a coded
// 402/403/405/429 response may carry beside "code": the server states which
// gate refused the request and what it was missing, but sends no human "detail"
// on these routes, so a caller such as cmd/iam's RBAC guidance table reads these
// instead. "missing" is accepted as either one scope string or a list of them—
// either way it comes back as a slice, trimmed and with empty entries dropped.
func parseGateAndMissing(parsed map[string]any) (gate string, missing []string) {
	gate = stringField(parsed, "gate")
	switch v := parsed["missing"].(type) {
	case string:
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			missing = []string{trimmed}
		}
	case []any:
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				continue
			}
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				missing = append(missing, trimmed)
			}
		}
	}
	return gate, missing
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
