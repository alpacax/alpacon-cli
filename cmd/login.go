package cmd

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
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

// knownCloudRegions are the Alpacon Cloud regions shown when --region is omitted.
// Source: 10-alpacon-web constants.ts, 06-account settings.py. Update on release.
var knownCloudRegions = []string{"us1", "ap1"}

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

Browser authentication is required. The CLI opens the browser automatically
and waits for completion. Do not use --no-browser unless running in a
headless environment (SSH, containers) where no browser is available.

  Alpacon Cloud:
    alpacon login                                      (interactive prompts)
    alpacon login --workspace <name> --region <region>  (non-interactive)

  Self-hosted:
    alpacon login <host>

Re-login: 'alpacon login' without arguments reuses the saved workspace.`,
	Example: `  # Alpacon Cloud login (interactive)
  alpacon login

  # Alpacon Cloud login (non-interactive, for CI/CD or AI agents)
  alpacon login --workspace myworkspace --region us1

  # Alpacon Cloud login with an API token
  alpacon login --workspace myworkspace --region us1 -t <api-token>

  # Self-hosted
  alpacon login alpacon.example.com

  # Self-hosted with an API token
  alpacon login alpacon.example.com -t <api-token>

  # Self-hosted with username and password
  alpacon login alpacon.example.com -u admin -p mypassword

  # Self-signed certificates
  alpacon login alpacon.example.com --insecure

  # Headless environment only (no browser available)
  alpacon login --no-browser
  ALPACON_NO_BROWSER=1 alpacon login`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		// Trim cloud flag values so whitespace-only input can't bypass
		// validateCloudFlags or assemble a broken URL like https://demo. .alpacon.io.
		var blank bool
		workspaceFlag, regionFlag, blank = normalizeCloudFlags(workspaceFlag, regionFlag)
		if blank {
			utils.CliErrorWithExit("--workspace and --region cannot be blank")
		}

		if len(args) > 0 && (workspaceFlag != "" || regionFlag != "") {
			utils.CliErrorWithExit("Cannot combine a HOST argument with --workspace/--region. Use a HOST for self-hosted, or --workspace/--region for Alpacon Cloud")
		}

		var workspaceURL, workspaceName, baseDomain string

		if len(args) > 0 {
			// Host mode: self-hosted only. Cloud direct URLs are deprecated.
			rejectURLWithPath(args[0])
			workspaceURL = formatHostURL(args[0])
			if isCloudDirectURL(workspaceURL) {
				utils.CliErrorWithExit("Direct URLs are not supported for Alpacon Cloud. Use 'alpacon login --workspace <name> --region <region>' instead")
			}
			workspaceName = utils.ExtractWorkspaceName(workspaceURL)
			baseDomain = utils.ExtractBaseDomain(workspaceURL)
		} else if workspaceFlag != "" || regionFlag != "" {
			// Alpacon Cloud mode via flags (non-interactive)
			if err := validateCloudFlags(workspaceFlag, regionFlag); err != nil {
				utils.CliErrorWithExit("%s", err.Error())
			}
			workspaceName = workspaceFlag
			baseDomain = defaultBaseDomain
			workspaceURL = buildCloudWorkspaceURL(workspaceFlag, regionFlag)
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
				// Interactive Alpacon Cloud prompts
				workspaceName = utils.PromptForRequiredInput("Workspace name: ")
				region := utils.PromptForInputWithDefault(
					fmt.Sprintf("Region [%s] (%s): ", knownCloudRegions[0], cloudRegionsHint()),
					knownCloudRegions[0],
				)
				baseDomain = defaultBaseDomain
				workspaceURL = buildCloudWorkspaceURL(workspaceName, region)
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

			fmt.Fprintf(os.Stderr, "\nPlease authenticate by visiting:\n%s\n\n", utils.Blue(deviceCode.VerificationURIComplete))
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
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}
		if err := ac.LoadCurrentUser(); err != nil {
			utils.CliErrorWithExit("Login succeeded but failed to verify user profile: %s. Please try logging in again.", err)
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
	loginCmd.Flags().StringVar(&workspaceFlag, "workspace", "", "Workspace name for Alpacon Cloud login")
	loginCmd.Flags().StringVar(&regionFlag, "region", "", "Region for Alpacon Cloud login (e.g., us1, ap1)")
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

func cloudRegionsHint() string {
	return strings.Join(knownCloudRegions, ", ")
}

// buildCloudWorkspaceURL assembles an Alpacon Cloud workspace URL.
func buildCloudWorkspaceURL(workspace, region string) string {
	return fmt.Sprintf("https://%s.%s.%s", workspace, region, defaultBaseDomain)
}

// normalizeCloudFlags trims flag values and reports whether the user supplied
// only blank (whitespace-only) values, which must be rejected rather than
// silently treated as if no flags were passed.
func normalizeCloudFlags(workspace, region string) (w, r string, blank bool) {
	w, r = strings.TrimSpace(workspace), strings.TrimSpace(region)
	blank = (workspace != "" || region != "") && w == "" && r == ""
	return w, r, blank
}

// validateCloudFlags rejects the case where exactly one of workspace/region is set.
func validateCloudFlags(workspace, region string) error {
	if workspace != "" && region == "" {
		return fmt.Errorf(
			"--region is required for Alpacon Cloud.\nAvailable regions: %s\nTry: alpacon login --workspace %s --region %s",
			cloudRegionsHint(), workspace, knownCloudRegions[0],
		)
	}
	if region != "" && workspace == "" {
		return fmt.Errorf(
			"--workspace is required for Alpacon Cloud.\nTry: alpacon login --workspace <name> --region %s",
			region,
		)
	}
	return nil
}

// isCloudDirectURL reports whether a host-mode URL targets the Alpacon Cloud base
// domain (alpacon.io or a subdomain). Such direct URLs are deprecated.
func isCloudDirectURL(workspaceURL string) bool {
	parsed, err := url.Parse(workspaceURL)
	if err != nil {
		return false
	}
	// Normalize for case-insensitive hostnames and the trailing-dot FQDN form.
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	return host == defaultBaseDomain || strings.HasSuffix(host, "."+defaultBaseDomain)
}

// rejectURLWithPath exits with a migration hint if the host argument contains a path.
// This catches the legacy alpacon.io/workspace format that is no longer supported.
// TODO: remove once the new workspace/region flow is well-established.
func rejectURLWithPath(host string) {
	raw := host
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return
	}
	if p := strings.TrimSuffix(parsed.Path, "/"); p != "" {
		utils.CliErrorWithExit("URL paths are not supported. For Alpacon Cloud use 'alpacon login --workspace <name> --region <region>'; for self-hosted pass the host without a path (e.g. 'alpacon login alpacon.example.com')")
	}
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
