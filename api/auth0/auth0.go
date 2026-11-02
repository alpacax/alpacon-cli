package auth0

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/httpclient"
	"github.com/alpacax/alpacon-cli/utils"
)

// rfc8628PollIntervalSeconds is both the default interval and the slow_down
// increment of RFC 8628 §3.5.
const rfc8628PollIntervalSeconds = 5

// maxDeviceCodeLifetimeSeconds keeps interval+slow_down and the seconds-to-Duration
// conversion from overflowing, including on 32-bit int.
const maxDeviceCodeLifetimeSeconds = math.MaxInt32 / 2

const (
	envPath        = "/api/auth/env"
	deviceCodePath = "/oauth/device/code"
	tokenPath      = "/oauth/token"
	revokePath     = "/oauth/revoke"
)

var (
	requestAccessTokenFunc = requestAccessToken

	// deviceTokenClient carries every device-code poll of one login and always verifies TLS.
	deviceTokenClient = httpclient.New(false)
)

// oauthError is the only refresh failure safe to retry with another scope; any other
// may have consumed the refresh token. mapAuth0Error matches on Error(), so keep its format.
type oauthError struct {
	Code       string
	Desc       string
	statusCode int
}

func (e *oauthError) Error() string {
	return fmt.Sprintf("error response from authentication server: %s - %s", e.Code, e.Desc)
}

// IsRefreshTokenRejected reports an OAuth refusal that requires a new login.
func IsRefreshTokenRejected(err error) bool {
	var oauthErr *oauthError
	if !errors.As(err, &oauthErr) || oauthErr.statusCode < http.StatusBadRequest || oauthErr.statusCode >= http.StatusInternalServerError {
		return false
	}
	// A rate limit is transient whatever code and description it carries.
	if oauthErr.statusCode == http.StatusTooManyRequests {
		return false
	}
	switch oauthErr.Code {
	case "invalid_grant", "unauthorized_client", "invalid_request", "invalid_client":
		return true
	case "access_denied":
		// Auth0 also uses access_denied for the rate limit described in
		// isDeviceScopeRefusal; a new login cannot resolve that refusal.
		return !strings.Contains(strings.ToLower(oauthErr.Desc), "global limit has been reached")
	default:
		return false
	}
}

func FetchAuthEnv(workspaceURL string, httpClient *http.Client) (*AuthEnvResponse, error) {
	apiURL := utils.BuildURL(workspaceURL, envPath, map[string]string{"client": "cli"})

	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", utils.GetUserAgent())

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, utils.WithRedirectStatus(err, resp)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode > http.StatusFound {
		return nil, fmt.Errorf("response status: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var env AuthEnvResponse
	err = json.Unmarshal(body, &env)
	if err != nil {
		return nil, err
	}

	return &env, nil
}

// resolveOrgName returns the Auth0 organization name for the org scope: the server's schema
// name, which survives a workspace URL rename, or fallback when an older server omits it.
func resolveOrgName(envInfo *AuthEnvResponse, fallback string) string {
	if envInfo != nil && envInfo.Auth0.SchemaName != "" {
		return envInfo.Auth0.SchemaName
	}
	return fallback
}

// appendDeviceScope appends a `device:<id>` scope so the server can bind an MFA presence proof
// to this installation instead of an IP-based fingerprint that installations behind one egress IP
// would share. It travels as a scope because /oauth/device/code accepts only client_id, scope
// and audience.
//
// A malformed or empty identifier is dropped: the server ignores one failing
// config.IsValidDeviceID, and a value carrying a space would inject a second scope. Existing
// scopes are never reordered or rewritten.
func appendDeviceScope(scope, deviceID string) string {
	if !config.IsValidDeviceID(deviceID) {
		return scope
	}
	return scope + " device:" + deviceID
}

func deviceCodeScope(orgName, deviceID string) string {
	return appendDeviceScope(fmt.Sprintf("openid profile email offline_access cli org:%s", orgName), deviceID)
}

// refreshScope composes the scope for the refresh-token grant. Auth0 re-runs post-login actions
// on a refresh, so the device identifier must be sent there too.
func refreshScope(orgName, deviceID string) string {
	return appendDeviceScope(fmt.Sprintf("cli org:%s", orgName), deviceID)
}

// currentDeviceID returns this installation's device identifier, or "" when it
// cannot be read or created. The identifier is an optional hardening signal, so
// a failure here degrades to the server-side fingerprint fallback instead of
// blocking the login.
func currentDeviceID() string {
	deviceID, err := config.GetOrCreateDeviceID()
	if err != nil {
		return ""
	}
	return deviceID
}

func RequestDeviceCode(workspaceName string, httpClient *http.Client, envInfo *AuthEnvResponse) (*DeviceCodeResponse, error) {
	scope := deviceCodeScope(resolveOrgName(envInfo, workspaceName), currentDeviceID())
	return requestDeviceCode(httpClient, envInfo, scope, envInfo.Auth0.Audience)
}

// RequestSignerDeviceCode authorizes the signer's audience with the login's org
// scope but no offline_access: the signer's API grants no refresh token.
func RequestSignerDeviceCode(workspaceName string, httpClient *http.Client, envInfo *AuthEnvResponse, audience string) (*DeviceCodeResponse, error) {
	scope := signerDeviceCodeScope(resolveOrgName(envInfo, workspaceName), currentDeviceID())
	return requestDeviceCode(httpClient, envInfo, scope, audience)
}

// signerDeviceCodeScope is deviceCodeScope without offline_access.
func signerDeviceCodeScope(orgName, deviceID string) string {
	return appendDeviceScope(fmt.Sprintf("openid profile email cli org:%s", orgName), deviceID)
}

func requestDeviceCode(httpClient *http.Client, envInfo *AuthEnvResponse, scope, audience string) (*DeviceCodeResponse, error) {
	data := map[string]string{
		"client_id": envInfo.Auth0.ClientID,
		"scope":     scope,
		"audience":  audience,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	apiURL := utils.BuildURL("https://"+envInfo.Auth0.Domain, deviceCodePath, nil)
	req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, utils.WithRedirectStatus(err, resp)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned error: %v", resp.StatusCode)
	}

	var deviceCode DeviceCodeResponse
	err = json.NewDecoder(resp.Body).Decode(&deviceCode)
	if err != nil {
		return nil, err
	}

	return &deviceCode, nil
}

func evaluatePollResponse(err error, interval int) (int, bool) {
	var oauthErr *oauthError
	if !errors.As(err, &oauthErr) {
		return 0, false
	}
	switch oauthErr.Code {
	case "authorization_pending":
		return interval, true
	case "slow_down":
		return interval + rfc8628PollIntervalSeconds, true
	default:
		return 0, false
	}
}

func devicePollInterval(interval int) int {
	if interval <= 0 {
		return rfc8628PollIntervalSeconds
	}
	return interval
}

func PollForToken(deviceCodeRes *DeviceCodeResponse, envInfo *AuthEnvResponse) (*TokenResponse, error) {
	startTime := time.Now()
	lifetime := min(max(deviceCodeRes.ExpiresIn, 0), maxDeviceCodeLifetimeSeconds)
	interval := min(devicePollInterval(deviceCodeRes.Interval), lifetime+1)
	timeoutErr := ErrDeviceCodeTimedOut

	spinner := utils.NewSpinner("Waiting for authentication...")
	spinner.Start()
	defer spinner.Stop()

	for {
		if time.Since(startTime).Seconds() > float64(lifetime) {
			return nil, timeoutErr
		}

		tokenResponse, err := requestAccessTokenFunc(deviceCodeRes.DeviceCode, envInfo)
		if err != nil {
			if next, retry := evaluatePollResponse(err, interval); retry {
				interval = next
				if time.Since(startTime).Seconds()+float64(interval) > float64(lifetime) {
					return nil, timeoutErr
				}
				time.Sleep(time.Duration(interval) * time.Second)
				continue
			}
			return nil, mapAuth0Error(err)
		}

		return tokenResponse, nil
	}
}

// Sentinels for the device-code endings a caller may word its own remedy for;
// the text is what 'alpacon login' prints.
var (
	ErrDeviceCodeTimedOut = errors.New("authentication timed out. Please restart the login process")
	ErrDeviceCodeDenied   = errors.New("authentication was denied. You may have cancelled the browser prompt")
	ErrDeviceCodeExpired  = errors.New("authentication session expired. Please run 'alpacon login' again")
)

func mapAuth0Error(err error) error {
	errStr := err.Error()
	switch {
	case strings.Contains(errStr, "access_denied"):
		return ErrDeviceCodeDenied
	case strings.Contains(errStr, "expired_token"):
		return ErrDeviceCodeExpired
	default:
		return err
	}
}

func RefreshAccessToken(workspaceURL string, httpClient *http.Client, refreshToken string) (*TokenResponse, error) {
	envInfo, err := FetchAuthEnv(workspaceURL, httpClient)
	if err != nil {
		return nil, err
	}

	orgName := resolveOrgName(envInfo, "")
	if orgName == "" {
		orgName, err = extractSubdomain(workspaceURL)
		if err != nil {
			return nil, err
		}
	}

	tokenRes, err := refreshWithDeviceScopeFallback(envInfo, httpClient, refreshToken, orgName)
	if err != nil {
		return nil, err
	}

	err = config.SaveRefreshedAuth0Token(refreshToken, tokenRes.AccessToken, tokenRes.ExpiresIn)
	if err != nil && !errors.Is(err, config.ErrLoginChanged) && !errors.Is(err, fs.ErrNotExist) {
		utils.CliWarning("Could not save the refreshed access token, so the next command will refresh it again: %s", err)
	}

	return tokenRes, nil
}

// refreshWithDeviceScopeFallback runs the refresh-token grant with the device scope and retries
// once without it when the server refuses that scope (see isDeviceScopeRefusal).
//
// A login made before the device scope shipped holds a refresh token granted without
// `device:<id>`, and RFC 6749 §6 lets the server reject a refresh asking for more than the
// original grant, so an unconditional device scope could lock out every such installation.
// Never sending it on refresh fails too: the server keys the stored MFA claim by whether the
// identifier was sent, so a login with it and a refresh without it lose presence. The fallback
// therefore runs per exchange and costs one extra round-trip only when the server refuses the
// scope.
func refreshWithDeviceScopeFallback(envInfo *AuthEnvResponse, httpClient *http.Client, refreshToken, orgName string) (*TokenResponse, error) {
	scope := refreshScope(orgName, currentDeviceID())
	fallbackScope := refreshScope(orgName, "")

	tokenRes, err := requestRefreshedToken(envInfo, httpClient, refreshToken, scope)
	if err == nil || scope == fallbackScope {
		// Either it worked, or there was no device scope to drop and a retry
		// would send the identical request.
		return tokenRes, err
	}

	// Retry only a refusal the server actually sent back, and only one whose
	// code says the scope is what it refused. A transport or decoding failure
	// leaves it unknown whether the exchange was processed, and replaying it
	// could spend a refresh token the server already rotated.
	var oauthErr *oauthError
	if !errors.As(err, &oauthErr) || !isDeviceScopeRefusal(oauthErr.Code) {
		return nil, err
	}

	utils.CliDebug("The authentication server rejected the refresh carrying this installation's device scope (%s). "+
		"Retrying without it; MFA presence falls back to the server-side fingerprint until the next 'alpacon login'.", oauthErr.Code)

	return requestRefreshedToken(envInfo, httpClient, refreshToken, fallbackScope)
}

// isDeviceScopeRefusal reports whether an OAuth error code is the server declining the requested
// scope—the one refusal dropping `device:<id>` can fix.
//
// It is an allowlist because the codes to skip are not enumerable: Auth0 reports a rate limit as
// `too_many_requests` and also as `access_denied` with "Global limit has been reached", and a
// retry against a throttled endpoint doubles its load.
//
//   - invalid_scope is RFC 6749 §5.2's name for a scope exceeding the original grant.
//   - invalid_request covers a server that treats an unknown scope token as a bad parameter. It is
//     speculative, kept because a needless retry costs one request while a missed refusal
//     degrades presence keying for the whole login.
//
// access_denied stays out: it also carries policy denials, and retrying without the identifier
// could turn a denial made because of the device into a success. Transient codes (server_error,
// temporarily_unavailable) and codes a scope cannot change (invalid_grant, invalid_client,
// unauthorized_client, unsupported_grant_type) stay out too. Add a code only once it is observed
// refusing this scope; a missed one silently degrades presence keying to the IP fingerprint for
// the life of the login.
func isDeviceScopeRefusal(code string) bool {
	switch code {
	case "invalid_scope", "invalid_request":
		return true
	default:
		return false
	}
}

// requestRefreshedToken exchanges a refresh token for a new access token under
// the given scope. It neither reads nor writes the stored config, so the caller
// can attempt more than one scope before committing a result.
func requestRefreshedToken(envInfo *AuthEnvResponse, httpClient *http.Client, refreshToken, scope string) (*TokenResponse, error) {
	data := map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     envInfo.Auth0.ClientID,
		"refresh_token": refreshToken,
		"scope":         scope,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	apiURL := utils.BuildURL("https://"+envInfo.Auth0.Domain, tokenPath, nil)
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, utils.WithRedirectStatus(err, resp)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var tokenRes TokenResponse
	err = json.Unmarshal(body, &tokenRes)
	if err != nil {
		return nil, err
	}

	if tokenRes.Error != "" {
		return nil, &oauthError{Code: tokenRes.Error, Desc: tokenRes.ErrorDesc, statusCode: resp.StatusCode}
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("response status: %s", resp.Status)
	}
	if tokenRes.AccessToken == "" {
		return nil, fmt.Errorf("authentication server returned an empty access token")
	}

	return &tokenRes, nil
}

func RevokeToken(httpClient *http.Client, workspaceURL string, refreshToken string) error {
	envInfo, err := FetchAuthEnv(workspaceURL, httpClient)
	if err != nil {
		return err
	}
	data := map[string]string{
		"client_id": envInfo.Auth0.ClientID,
		"token":     refreshToken,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}

	apiURL := utils.BuildURL("https://"+envInfo.Auth0.Domain, revokePath, nil)

	req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return utils.WithRedirectStatus(err, resp)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to revoke token. Status: %s, Body: %s", resp.Status, string(bodyBytes))
	}

	return nil
}

func requestAccessToken(deviceCode string, envInfo *AuthEnvResponse) (*TokenResponse, error) {
	data := map[string]string{
		"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
		"device_code": deviceCode,
		"client_id":   envInfo.Auth0.ClientID,
	}
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	apiURL := utils.BuildURL("https://"+envInfo.Auth0.Domain, tokenPath, nil)
	req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := deviceTokenClient.Do(req)
	if err != nil {
		return nil, utils.WithRedirectStatus(err, resp)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var tokenRes TokenResponse
	err = json.Unmarshal(body, &tokenRes)
	if err != nil {
		return nil, err
	}

	if tokenRes.Error != "" {
		return nil, &oauthError{Code: tokenRes.Error, Desc: tokenRes.ErrorDesc}
	}

	return &tokenRes, nil
}

func extractSubdomain(workspaceURL string) (string, error) {
	parsedURL, err := url.Parse(workspaceURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL format: %v", err)
	}

	parts := strings.Split(parsedURL.Host, ".")
	if len(parts) < 3 {
		return "", fmt.Errorf("invalid workspace URL: Subdomain is required")
	}

	return parts[0], nil
}

func Logout(httpClient *http.Client, validConfig config.Config) error {
	if validConfig.AccessToken != "" && validConfig.RefreshToken != "" {
		err := RevokeToken(httpClient, validConfig.WorkspaceURL, validConfig.RefreshToken)
		if err != nil {
			return fmt.Errorf("failed to revoke token: %v", err)
		}
	}
	err := config.DeleteConfig(validConfig)
	if err != nil {
		return err
	}
	return nil
}
