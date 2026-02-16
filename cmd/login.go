package cmd

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/api/auth0"
	"github.com/alpacax/alpacon-cli/api/workspace"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var (
	insecure bool
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in to Alpacon",
	Long:  "Log in to Alpacon. To access Alpacon, workspace url is must specified",
	Example: `
	# Re-login to saved workspace
	alpacon login

	# Cloud login (portal URL or API URL)
	alpacon login https://alpacon.io/myworkspace
	alpacon login myworkspace.us1.alpacon.io

	# Self-hosted
	alpacon login alpacon.example.com

	# Login via API Token
	alpacon login myworkspace.us1.alpacon.io -t [TOKEN_KEY]

	# Legacy username/password
	alpacon login [WORKSPACE_URL] -u [USERNAME] -p [PASSWORD]

	# Skip TLS certificate verification
	alpacon login [WORKSPACE_URL] --insecure
	`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var workspaceURL string

		// Determine the workspace URL to use
		if len(args) > 0 {
			workspaceURL = args[0]
		}

		if workspaceURL == "" {
			cfg, err := config.LoadConfig()
			if err == nil && cfg.WorkspaceURL != "" {
				workspaceURL = cfg.WorkspaceURL
			}
		}

		if workspaceURL == "" {
			workspaceURL = utils.PromptForRequiredInput("Workspace URL (e.g., alpacon.io/myworkspace or myworkspace.us1.alpacon.io): ")
		}

		httpClient := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					MinVersion:         tls.VersionTLS12,
					InsecureSkipVerify: insecure,
				},
			},
		}

		// Validate workspaceURL
		workspaceURL, err := validateAndFormatWorkspaceURL(workspaceURL, httpClient)
		if err != nil {
			utils.CliErrorWithExit("%s", err.Error())
		}

		// Check if this is a cloud app URL that needs post-login region resolution
		isAppURL := isCloudAppURL(workspaceURL)

		// Extract workspace name
		var workspaceName string
		if isAppURL {
			parsedURL, parseErr := url.Parse(workspaceURL)
			if parseErr != nil {
				utils.CliErrorWithExit("Invalid workspace URL: %s", parseErr.Error())
			}

			pathSegment := strings.Trim(parsedURL.Path, "/")
			segments := strings.Split(pathSegment, "/")
			if len(segments) != 1 || segments[0] == "" {
				utils.CliErrorWithExit("Invalid workspace URL: path must contain exactly one workspace name")
			}
			workspaceName = segments[0]
		} else {
			workspaceName = utils.ExtractWorkspaceName(workspaceURL)
		}

		// Fetch auth environment info
		// For cloud app URLs, use the app base URL (e.g., https://alpacon.io)
		authEnvURL := workspaceURL
		if isAppURL {
			parsedURL, _ := url.Parse(workspaceURL)
			authEnvURL = fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)
		}

		envInfo, err := auth0.FetchAuthEnv(authEnvURL, httpClient)
		if err != nil {
			if strings.Contains(err.Error(), "404") {
				envInfo = &auth0.AuthEnvResponse{
					Auth0: auth0.Auth0Config{
						Method: "legacy",
					},
				}
			} else if isAppURL {
				utils.CliErrorWithExit("Failed to fetch auth config. Try using the direct API URL format (e.g., %s.<region>.alpacon.io): %v", workspaceName, err)
			} else {
				utils.CliErrorWithExit("Failed to fetch environment variables from workspace: %v", err)
			}
		}

		username, _ := cmd.Flags().GetString("username")
		password, _ := cmd.Flags().GetString("password")
		token, _ := cmd.Flags().GetString("token")

		fmt.Printf("Logging in to %s\n", workspaceURL)

		if envInfo.Auth0.Method == "auth0" && token == "" {
			deviceCode, err := auth0.RequestDeviceCode(workspaceName, httpClient, envInfo)
			if err != nil {
				utils.CliErrorWithExit("Device code request failed. %v", err)
			}

			highlight := "\033[1;34m" // blue + bold
			reset := "\033[0m"

			fmt.Println("\n==================== AUTHENTICATION REQUIRED ====================")
			fmt.Println("\nPlease authenticate by visiting the following URL:")
			fmt.Printf("%s%s%s\n\n", highlight, deviceCode.VerificationURIComplete, reset)
			fmt.Print("===============================================================\n\n")

			tokenRes, err := auth0.PollForToken(deviceCode, envInfo)
			if err != nil {
				utils.CliErrorWithExit("%s", err.Error())
			}

			// For cloud app URLs, resolve the correct region-specific workspace URL from JWT
			if isAppURL {
				resolvedURL, resolvedName, resolveErr := workspace.ResolveWorkspaceURL(tokenRes.AccessToken, workspaceName, "alpacon.io")
				if resolveErr != nil {
					utils.CliErrorWithExit("Failed to resolve workspace region: %v", resolveErr)
				}

				// Verify the resolved URL is reachable
				resp, httpErr := httpClient.Get(resolvedURL)
				if httpErr != nil {
					utils.CliErrorWithExit("Resolved workspace URL '%s' is unreachable: %v", resolvedURL, httpErr)
				}
				if resp.StatusCode >= 400 {
					_ = resp.Body.Close()
					utils.CliErrorWithExit("Resolved workspace URL '%s' returned HTTP %d. Please check your connection", resolvedURL, resp.StatusCode)
				}
				_ = resp.Body.Close()

				workspaceURL = resolvedURL
				workspaceName = resolvedName
				fmt.Printf("Workspace resolved to %s\n", workspaceURL)
			}

			baseDomain := utils.ExtractBaseDomain(workspaceURL)

			err = config.CreateConfig(workspaceURL, workspaceName, "", "", tokenRes.AccessToken, tokenRes.RefreshToken, baseDomain, tokenRes.ExpiresIn, insecure)
			if err != nil {
				utils.CliErrorWithExit("Failed to save config: %v", err)
			}

		} else {
			// Legacy or token login — requires a direct API URL
			if isAppURL {
				utils.CliErrorWithExit("Cloud app URL format is not supported for token or legacy login. Use the direct API URL format: %s.<region>.alpacon.io", workspaceName)
			}

			if (workspaceURL == "" || username == "" || password == "") && token == "" {
				workspaceURL, username, password = promptForCredentials(workspaceURL, username, password)
			}

			loginRequest := &auth.LoginRequest{
				WorkspaceURL: workspaceURL,
				Username:     username,
				Password:     password,
			}

			err = auth.LoginAndSaveCredentials(loginRequest, token, insecure)
			if err != nil {
				utils.CliErrorWithExit("Login failed: %v. Please verify your username, password, and workspace URL are correct. If using a token, ensure it's valid and has not expired", err)
			}

		}
		_, err = client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		fmt.Println("Login succeeded!")
	},
}

func init() {
	var username, password, token string

	loginCmd.Flags().StringVarP(&username, "username", "u", "", "Username for login")
	loginCmd.Flags().StringVarP(&password, "password", "p", "", "Password for login")
	loginCmd.Flags().StringVarP(&token, "token", "t", "", "API token for login")
	loginCmd.Flags().BoolVar(&insecure, "insecure", false, "Skip TLS certificate verification")
}

func promptForCredentials(workspaceURL, username, password string) (string, string, string) {
	if workspaceURL == "" {
		configFile, err := config.LoadConfig()
		if err == nil && configFile.WorkspaceURL != "" {
			workspaceURL = configFile.WorkspaceURL
			fmt.Printf("Using Workspace URL %s from config file.\n", configFile.WorkspaceURL)
			fmt.Println("If you want to change the workspace, specify workspace url: alpacon login [WORKSPACE_URL] -u [USERNAME] -p [PASSWORD]")
			fmt.Println()
		}
	}

	if username == "" {
		username = utils.PromptForRequiredInput("Username: ")
	}

	if password == "" {
		password = utils.PromptForPassword("Password: ")
	}

	return workspaceURL, username, password
}

// isCloudAppURL checks if the URL is an alpacon.io app URL with a workspace path.
// e.g., "https://alpacon.io/myworkspace" → true
// e.g., "https://dev.alpacon.io/myworkspace" → false (dev has its own region handling)
// e.g., "https://myws.us1.alpacon.io" → false (already a direct API URL)
func isCloudAppURL(workspaceURL string) bool {
	parsedURL, err := url.Parse(workspaceURL)
	if err != nil {
		return false
	}
	return parsedURL.Host == "alpacon.io" && parsedURL.Path != "" && parsedURL.Path != "/"
}

func validateAndFormatWorkspaceURL(workspaceURL string, httpClient *http.Client) (string, error) {
	if !strings.HasPrefix(workspaceURL, "http") {
		workspaceURL = "https://" + workspaceURL
	}

	workspaceURL = strings.TrimSuffix(workspaceURL, "/")

	// Transform URL patterns: domain.com/workspace -> workspace.domain.com
	parsedURL, err := url.Parse(workspaceURL)
	if err == nil && parsedURL.Path != "" && parsedURL.Path != "/" {
		workspace := strings.TrimPrefix(parsedURL.Path, "/")
		domain := parsedURL.Host
		protocol := parsedURL.Scheme

		// For alpacon.io app URLs, the region is unknown at login time.
		// Keep the app URL as-is; the login flow will resolve the correct
		// region-specific API URL from the JWT after authentication.
		if domain == "alpacon.io" {
			return workspaceURL, nil
		}

		if domain != "" && workspace != "" {
			workspaceURL = fmt.Sprintf("%s://%s.%s", protocol, workspace, domain)
		}
	}

	// Validate that a workspace name is present for cloud domains.
	// e.g., "dev.alpacon.io" (3 parts) is missing a workspace — need "myws.dev.alpacon.io" (4+ parts).
	parsedURL, err = url.Parse(workspaceURL)
	if err == nil {
		hostname := parsedURL.Hostname()
		parts := strings.Split(hostname, ".")
		if len(parts) >= 2 && parts[len(parts)-2]+"."+parts[len(parts)-1] == "alpacon.io" && len(parts) < 4 {
			return "", fmt.Errorf("workspace name is missing from URL. Use the format: alpacon.io/<workspace> or <workspace>.%s", hostname)
		}
	}

	resp, err := httpClient.Get(workspaceURL)
	if err != nil || resp.StatusCode >= 400 {
		return "", fmt.Errorf("workspace URL '%s' is unreachable or invalid. Please check the URL and your internet connection", workspaceURL)
	}
	defer func() { _ = resp.Body.Close() }()

	return workspaceURL, nil
}
