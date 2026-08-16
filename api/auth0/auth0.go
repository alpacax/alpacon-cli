package auth0

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
)

var path = struct {
	env        string
	deviceCode string
	token      string
	revoke     string
}{
	env:        "/api/auth/env",
	deviceCode: "/oauth/device/code",
	token:      "/oauth/token",
	revoke:     "/oauth/revoke",
}

// oauthError is an error the token endpoint returned in its response body, as
// opposed to a transport, decoding or request-construction failure.
//
// The distinction matters on the refresh exchange: only a decision the server
// actually made is worth retrying with a different scope, and a request that
// may never have been answered must not be replayed against a refresh token the
// server may already have consumed. Its message is unchanged from the string
// this package produced before the type existed, because mapAuth0Error and
// PollForToken both match on it.
type oauthError struct {
	Code string
	Desc string
}

func (e *oauthError) Error() string {
	return fmt.Sprintf("error response from authentication server: %s - %s", e.Code, e.Desc)
}

func FetchAuthEnv(workspaceURL string, httpClient *http.Client) (*AuthEnvResponse, error) {
	apiURL := utils.BuildURL(workspaceURL, path.env, map[string]string{"client": "cli"})

	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", utils.GetUserAgent())

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
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

// resolveOrgName returns the Auth0 organization hint used in OAuth scopes.
// It prefers the server-provided schema name—the frozen workspace identity
// that equals the Auth0 organization name—so logins keep working after a
// workspace URL label changes. Older servers omit the field; fall back to
// the caller-derived label.
func resolveOrgName(envInfo *AuthEnvResponse, fallback string) string {
	if envInfo != nil && envInfo.Auth0.SchemaName != "" {
		return envInfo.Auth0.SchemaName
	}
	return fallback
}

// appendDeviceScope appends a `device:<id>` scope identifying this CLI
// installation, so the server can bind an MFA presence proof to the client that
// requested the challenge instead of falling back to an IP-based fingerprint
// that two installations behind one egress IP would share.
//
// It travels as a scope because /oauth/device/code accepts only client_id,
// scope and audience—there is no other field to put it in—and encoding a value
// as a scope is Auth0's documented answer for this flow. The `org:<name>` scope
// already reaches the `CLI Auth` action the same way.
//
// A malformed or empty identifier is dropped rather than sent. The `Add
// Completed MFA Claim` action ignores anything failing config.IsValidDeviceID
// and falls back to the fingerprint, so sending one would cost the hardening
// signal and gain nothing—and a value carrying a space would inject a second
// scope on the way. Existing scopes are never reordered or rewritten.
func appendDeviceScope(scope, deviceID string) string {
	if !config.IsValidDeviceID(deviceID) {
		return scope
	}
	return scope + " device:" + deviceID
}

// deviceCodeScope composes the scope string for the device-code request.
func deviceCodeScope(orgName, deviceID string) string {
	return appendDeviceScope(fmt.Sprintf("openid profile email offline_access cli org:%s", orgName), deviceID)
}

// refreshScope composes the scope string for the refresh-token grant. Auth0
// re-runs post-login actions on a refresh exchange, so the device identifier
// has to be present there too or the action loses it on every token refresh.
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
	data := map[string]string{
		"client_id": envInfo.Auth0.ClientID,
		"scope":     deviceCodeScope(resolveOrgName(envInfo, workspaceName), currentDeviceID()),
		"audience":  envInfo.Auth0.Audience,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	apiURL := utils.BuildURL("https://"+envInfo.Auth0.Domain, path.deviceCode, nil)
	req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
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

func PollForToken(deviceCodeRes *DeviceCodeResponse, envInfo *AuthEnvResponse) (*TokenResponse, error) {
	startTime := time.Now()

	spinner := utils.NewSpinner("Waiting for authentication...")
	spinner.Start()
	defer spinner.Stop()

	for {
		if time.Since(startTime).Seconds() > float64(deviceCodeRes.ExpiresIn) {
			return nil, fmt.Errorf("authentication timed out. Please restart the login process")
		}

		tokenResponse, err := requestAccessToken(deviceCodeRes.DeviceCode, envInfo)
		if err != nil {
			if strings.Contains(err.Error(), "authorization_pending") {
				time.Sleep(time.Duration(deviceCodeRes.Interval) * time.Second)
				continue
			}
			return nil, mapAuth0Error(err)
		}

		return tokenResponse, nil
	}
}

// mapAuth0Error converts OAuth error codes to user-friendly messages.
func mapAuth0Error(err error) error {
	errStr := err.Error()
	switch {
	case strings.Contains(errStr, "access_denied"):
		return fmt.Errorf("authentication was denied. You may have cancelled the browser prompt")
	case strings.Contains(errStr, "expired_token"):
		return fmt.Errorf("authentication session expired. Please run 'alpacon login' again")
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

	err = config.SaveRefreshedAuth0Token(tokenRes.AccessToken, tokenRes.ExpiresIn)
	if err != nil {
		return nil, err
	}

	return tokenRes, nil
}

// refreshWithDeviceScopeFallback runs the refresh-token grant with the device
// scope and retries once without it when the server refuses the exchange.
//
// Every installation that logged in before the device scope shipped holds a
// refresh token granted against a scope set that never contained
// `device:<id>`, and the identifier is minted on the first run after the
// upgrade. RFC 6749 §6 tells an authorization server to reject a refresh whose
// requested scope is not within the original grant, and what Auth0 does with
// this particular pseudo-scope on that exchange is not something the CLI can
// establish ahead of time. If it rejects, an unconditional request locks out
// every logged-in installation at once.
//
// Dropping the scope on refresh instead is not open to the CLI. The post-login
// action partitions the stored MFA claim by how the identifier was supplied:
// `mfa_c_<id>` when the client sent one, `mfa_<fingerprint>` when it did not,
// and its refresh branch restores the claim from that same key. A login that
// sends the identifier and a refresh that does not therefore resolve to
// different partitions, and presence is lost on every refresh.
//
// So the fallback runs per exchange rather than being decided once: an
// installation that predates the scope pays one extra round-trip and degrades
// to the fingerprint keying it already had, and one that logged in after the
// upgrade never reaches the retry. The worst case is today's behavior.
func refreshWithDeviceScopeFallback(envInfo *AuthEnvResponse, httpClient *http.Client, refreshToken, orgName string) (*TokenResponse, error) {
	scope := refreshScope(orgName, currentDeviceID())
	fallbackScope := refreshScope(orgName, "")

	tokenRes, err := requestRefreshedToken(envInfo, httpClient, refreshToken, scope)
	if err == nil || scope == fallbackScope {
		// Either it worked, or there was no device scope to drop and a retry
		// would send the identical request.
		return tokenRes, err
	}

	// Retry only a refusal the server actually sent back. A transport or
	// decoding failure leaves it unknown whether the exchange was processed,
	// and replaying it could spend a refresh token the server already rotated.
	var oauthErr *oauthError
	if !errors.As(err, &oauthErr) {
		return nil, err
	}

	utils.CliDebug("The authentication server rejected the refresh carrying this installation's device scope (%s). "+
		"Retrying without it; MFA presence falls back to the server-side fingerprint until the next 'alpacon login'.", oauthErr.Code)

	return requestRefreshedToken(envInfo, httpClient, refreshToken, fallbackScope)
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

	apiURL := utils.BuildURL("https://"+envInfo.Auth0.Domain, path.token, nil)
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
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

	apiURL := utils.BuildURL("https://"+envInfo.Auth0.Domain, path.revoke, nil)

	req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
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

	apiURL := utils.BuildURL("https://"+envInfo.Auth0.Domain, path.token, nil)
	req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
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
	err := config.DeleteConfig()
	if err != nil {
		return err
	}
	return nil
}
