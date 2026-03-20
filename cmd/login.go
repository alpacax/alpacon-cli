package cmd

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/api/auth0"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

const defaultBaseDomain = "alpacon.io"

var (
	insecure      bool
	noBrowser     bool
	workspaceFlag string
	regionFlag    string
)

var loginCmd = &cobra.Command{
	Use:   "login [HOST]",
	Short: "Log in to Alpacon",
	Long: `Log in to Alpacon.

There are two ways to log in depending on your deployment:

  SaaS (Alpacon Cloud):
    Run 'alpacon login' without arguments. You will be prompted for your
    workspace name and region, then authenticate via browser.

    For non-interactive environments (CI/CD pipelines, AI agents), use flags:
      alpacon login --workspace <name> --region <region>

  Self-hosted:
    Provide the host as an argument:
      alpacon login <host>

If you have previously logged in, running 'alpacon login' without arguments
will re-authenticate to your saved workspace.

The browser opens automatically for authentication. Use --no-browser to
disable this. To suppress browser opens globally (including MFA prompts),
set ALPACON_NO_BROWSER=1.`,
	Example: `  # SaaS login (interactive prompts)
  alpacon login

  # SaaS login for CI/CD or AI agents (non-interactive)
  alpacon login --workspace myworkspace --region ap1

  # Self-hosted instance
  alpacon login alpacon.example.com

  # Direct API URL (backward compatible)
  alpacon login myworkspace.us1.alpacon.io

  # Login with API token (self-hosted or direct URL)
  alpacon login myworkspace.us1.alpacon.io -t <api-token>

  # Login with username and password
  alpacon login myworkspace.us1.alpacon.io -u admin -p mypassword

  # Skip TLS certificate verification (self-signed certs)
  alpacon login alpacon.example.com --insecure

  # Disable automatic browser open
  alpacon login --no-browser
  ALPACON_NO_BROWSER=1 alpacon login`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var workspaceURL, workspaceName, baseDomain string

		if len(args) > 0 {
			// Host mode: self-hosted, local dev, or direct API URL
			workspaceURL = formatHostURL(args[0])
			workspaceName = utils.ExtractWorkspaceName(workspaceURL)
			baseDomain = utils.ExtractBaseDomain(workspaceURL)
		} else if workspaceFlag != "" || regionFlag != "" {
			// SaaS mode via flags (non-interactive)
			workspaceName = workspaceFlag
			baseDomain = defaultBaseDomain
			workspaceURL = fmt.Sprintf("https://%s.%s.%s", workspaceFlag, regionFlag, defaultBaseDomain)
		} else {
			// No args, no flags — try saved config for re-login
			cfg, err := config.LoadConfig()
			if err == nil && cfg.WorkspaceURL != "" {
				workspaceURL = cfg.WorkspaceURL
				workspaceName = cfg.WorkspaceName
				baseDomain = cfg.BaseDomain
				if baseDomain == "" {
					baseDomain = utils.ExtractBaseDomain(workspaceURL)
				}
				utils.CliInfo("Using saved workspace: %s", workspaceURL)
			} else {
				// Interactive SaaS prompts
				workspaceName = utils.PromptForRequiredInput("Workspace: ")
				region := utils.PromptForInputWithDefault("Region (default: us1): ", "us1")
				baseDomain = defaultBaseDomain
				workspaceURL = fmt.Sprintf("https://%s.%s.%s", workspaceName, region, defaultBaseDomain)
			}
		}

		httpClient := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					MinVersion:         tls.VersionTLS12,
					InsecureSkipVerify: insecure,
				},
			},
		}

		envInfo, err := auth0.FetchAuthEnv(workspaceURL, httpClient)
		if err != nil {
			if strings.Contains(err.Error(), "404") {
				utils.CliErrorWithExit("Workspace not found. Please check your workspace name and region")
			}
			utils.CliErrorWithExit("Workspace '%s' is unreachable or returned an error: %v", workspaceURL, err)
		}

		username, _ := cmd.Flags().GetString("username")
		password, _ := cmd.Flags().GetString("password")
		token, _ := cmd.Flags().GetString("token")

		utils.CliInfo("Logging in to %s", workspaceURL)

		if envInfo.Auth0.Method == "auth0" && token == "" {
			deviceCode, err := auth0.RequestDeviceCode(workspaceName, httpClient, envInfo)
			if err != nil {
				utils.CliErrorWithExit("Device code request failed. %v", err)
			}

			fmt.Fprintf(os.Stderr, "\nPlease authenticate by visiting:\n  %s\n\n", utils.Blue(deviceCode.VerificationURIComplete))
			fmt.Fprintf(os.Stderr, "Verification code: %s\n\n", utils.Bold(deviceCode.UserCode))
			if !noBrowser {
				utils.OpenBrowser(deviceCode.VerificationURIComplete)
			}

			tokenRes, err := auth0.PollForToken(deviceCode, envInfo)
			if err != nil {
				utils.CliErrorWithExit("%s", err.Error())
			}

			err = config.CreateConfig(workspaceURL, workspaceName, "", "", tokenRes.AccessToken, tokenRes.RefreshToken, baseDomain, tokenRes.ExpiresIn, insecure)
			if err != nil {
				utils.CliErrorWithExit("Failed to save config: %v", err)
			}

		} else {
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
				if strings.Contains(err.Error(), "404") {
					utils.CliErrorWithExit("Login endpoint not found. This workspace may not support username/password login")
				}
				utils.CliErrorWithExit("Login failed: %v. Please verify your username, password, and workspace URL are correct. If using a token, ensure it's valid and has not expired", err)
			}

		}
		_, err = client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		utils.CliSuccess("Login succeeded!")
	},
}

func init() {
	var username, password, token string

	loginCmd.Flags().StringVarP(&username, "username", "u", "", "Username for login")
	loginCmd.Flags().StringVarP(&password, "password", "p", "", "Password for login")
	loginCmd.Flags().StringVarP(&token, "token", "t", "", "API token for login")
	loginCmd.Flags().BoolVar(&insecure, "insecure", false, "Skip TLS certificate verification")
	loginCmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Do not open the browser automatically")
	loginCmd.Flags().StringVar(&workspaceFlag, "workspace", "", "Workspace name for SaaS login")
	loginCmd.Flags().StringVar(&regionFlag, "region", "", "Region for SaaS login (e.g., ap1, us1)")
	loginCmd.MarkFlagsRequiredTogether("workspace", "region")
}

func promptForCredentials(workspaceURL, username, password string) (string, string, string) {
	if workspaceURL == "" {
		configFile, err := config.LoadConfig()
		if err == nil && configFile.WorkspaceURL != "" {
			workspaceURL = configFile.WorkspaceURL
			utils.CliInfo("Using saved workspace: %s", configFile.WorkspaceURL)
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

// formatHostURL normalizes a host argument into a full URL.
// localhost and 127.0.0.1 default to http://, everything else to https://.
func formatHostURL(host string) string {
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return strings.TrimSuffix(host, "/")
	}
	scheme := "https"
	if strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.0.0.1") {
		scheme = "http"
	}
	return fmt.Sprintf("%s://%s", scheme, strings.TrimSuffix(host, "/"))
}
