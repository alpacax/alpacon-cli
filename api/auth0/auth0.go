package auth0

import (
	"bytes"
	"encoding/json"
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

func RequestDeviceCode(workspaceURL string, httpClient *http.Client, envInfo *AuthEnvResponse) (*DeviceCodeResponse, error) {
	subDomain, err := extractSubdomain(workspaceURL)
	if err != nil {
		return nil, err
	}

	data := map[string]string{
		"client_id": envInfo.Auth0.ClientID,
		"scope":     fmt.Sprintf("openid profile email offline_access cli org:%s", subDomain),
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

	for {
		if time.Since(startTime).Seconds() > float64(deviceCodeRes.ExpiresIn) {
			return nil, fmt.Errorf("authentication timed out. Please restart the login process")
		}

		tokenResponse, err := requestAccessToken(deviceCodeRes.DeviceCode, envInfo)
		if err != nil {
			if strings.Contains(err.Error(), "authorization_pending") {
				fmt.Println("Waiting for user to complete authentication...")
				time.Sleep(time.Duration(deviceCodeRes.Interval) * time.Second)
				continue
			}
			return nil, err
		}

		return tokenResponse, nil
	}
}

func RefreshAccessToken(workspaceURL string, httpClient *http.Client, refreshToken string) (*TokenResponse, error) {
	envInfo, err := FetchAuthEnv(workspaceURL, httpClient)
	if err != nil {
		return nil, err
	}

	subDomain, err := extractSubdomain(workspaceURL)
	if err != nil {
		return nil, err
	}

	data := map[string]string{
		"grant_type":    "refresh_token",
		"client_id":     envInfo.Auth0.ClientID,
		"refresh_token": refreshToken,
		"scope":         fmt.Sprintf("cli org:%s", subDomain),
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
		return nil, fmt.Errorf("error response from authentication server: %s - %s", tokenRes.Error, tokenRes.ErrorDesc)
	}

	err = config.SaveRefreshedAuth0Token(tokenRes.AccessToken, tokenRes.ExpiresIn)
	if err != nil {
		return nil, err
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
		return nil, fmt.Errorf("error response from authentication server: %s - %s", tokenRes.Error, tokenRes.ErrorDesc)
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
