package cmd

import (
	"crypto/tls"
	"errors"
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

Browser authentication is the default interactive flow. The CLI opens the
browser automatically and waits for completion. Use -t with an explicit
target for CI/CD or automation. Do not use --no-browser unless running in a
headless environment (SSH, containers) where no browser is available.

  Alpacon Cloud:
    alpacon login                                      (interactive prompts)
    alpacon login --workspace <name> --region <region>  (explicit target)

  Self-hosted:
    alpacon login <host>

Re-login: in an interactive shell, 'alpacon login' without arguments prompts
with the saved target as the default. Non-interactive login requires a HOST or
--workspace/--region.`,
	Example: `  # Alpacon Cloud login (interactive)
  alpacon login

  # Alpacon Cloud login with an explicit target
  alpacon login --workspace myworkspace --region us1

  # Alpacon Cloud login with an API token (for CI/CD or AI agents)
  alpacon login --workspace myworkspace --region us1 -t <api-token>

  # Self-hosted
  alpacon login alpacon.example.com

  # Self-hosted with an API token
  alpacon login alpacon.example.com -t <api-token>

  # Self-hosted with username and password
  alpacon login alpacon.example.com -u admin -p mypassword

  # Self-signed certificates
  alpacon login alpacon.example.com --insecure

  # Manual headless login (no browser available)
  alpacon login --workspace myworkspace --region us1 --no-browser
  ALPACON_NO_BROWSER=1 alpacon login --workspace myworkspace --region us1

  # Alpacon Cloud via direct URL with an API token (deprecated; prefer --workspace/--region)
  alpacon login myworkspace.us1.alpacon.io -t <api-token>`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		workspaceURL, workspaceName, baseDomain, ok, err := resolveLoginTarget(args, workspaceFlag, regionFlag)
		if err != nil {
			utils.CliErrorWithExit("%s", err.Error())
		}

		if !ok {
			if err := validateInteractiveLoginTargetPrompt(utils.IsInteractiveShell()); err != nil {
				utils.CliErrorWithExit("%s", err.Error())
			}
			cfg, _ := config.LoadConfig()
			workspaceURL, workspaceName, baseDomain, err = promptForLoginTarget(cfg)
			if err != nil {
				utils.CliErrorWithExit("%s", err.Error())
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
			if (username == "" || password == "") && token == "" {
				username, password = promptForCredentials(username, password)
			}

			loginRequest := &auth.LoginRequest{
				WorkspaceURL:  workspaceURL,
				Username:      username,
				Password:      password,
				WorkspaceName: workspaceName,
				BaseDomain:    baseDomain,
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
			if shouldFailOnProfileError(token) {
				utils.CliErrorWithExit("Login succeeded but failed to verify user profile: %s. Please try logging in again.", err)
			}
			if config.IsServiceToken(token) {
				utils.CliInfo("Authenticated with a service token. Service tokens are application principals and have no user profile, so user details are unavailable.")
			} else {
				utils.CliInfo("Could not preload your user profile; continuing since the credential was verified.")
			}
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

func promptForLoginTarget(cfg config.Config) (workspaceURL, workspaceName, baseDomain string, err error) {
	return promptForLoginTargetWithPrompts(cfg, utils.PromptForInputWithDefault, utils.PromptForRequiredInput)
}

func promptForLoginTargetWithPrompts(
	cfg config.Config,
	promptWithDefault func(promptText, defaultValue string) string,
	promptRequired func(promptText string) string,
) (workspaceURL, workspaceName, baseDomain string, err error) {
	if cfg.WorkspaceURL != "" && !isCloudWorkspaceURL(cfg.WorkspaceURL) {
		savedURL := formatHostURL(cfg.WorkspaceURL)
		host := promptWithDefault(
			fmt.Sprintf("Host [%s]: ", savedURL),
			savedURL,
		)
		if err := validateHostTarget(host); err != nil {
			return "", "", "", err
		}
		workspaceURL = formatHostURL(host)
		baseDomain = utils.ExtractBaseDomain(workspaceURL)
		if hostBelongsToBaseDomain(workspaceURL, cfg.BaseDomain) {
			baseDomain = cfg.BaseDomain
		}
		workspaceName = utils.ExtractWorkspaceName(workspaceURL)
		if workspaceURL == savedURL && cfg.WorkspaceName != "" {
			workspaceName = cfg.WorkspaceName
		}
		return workspaceURL, workspaceName, baseDomain, nil
	}

	defaultWorkspace, defaultRegion := cloudLoginDefaults(cfg)
	if defaultWorkspace == "" {
		workspaceName = promptRequired("Workspace name: ")
	} else {
		workspaceName = promptWithDefault(
			fmt.Sprintf("Workspace name [%s]: ", defaultWorkspace),
			defaultWorkspace,
		)
	}

	region := promptWithDefault(
		fmt.Sprintf("Region [%s] (%s): ", defaultRegion, cloudRegionsHint()),
		defaultRegion,
	)
	workspaceName = strings.TrimSpace(workspaceName)
	region = strings.TrimSpace(region)
	if err := validateCloudTargetParts(workspaceName, region); err != nil {
		return "", "", "", err
	}
	return buildCloudWorkspaceURL(workspaceName, region), workspaceName, defaultBaseDomain, nil
}

func validateInteractiveLoginTargetPrompt(isInteractive bool) error {
	if isInteractive {
		return nil
	}
	return errors.New("login target is required in non-interactive mode. Pass a HOST or use --workspace and --region; use -t with an explicit target for CI/CD or automation")
}

func promptForCredentials(username, password string) (string, string) {
	if username == "" {
		username = utils.PromptForRequiredInput("Username: ")
	}

	if password == "" {
		password = utils.PromptForPassword("Password: ")
	}

	return username, password
}

func cloudRegionsHint() string {
	return strings.Join(knownCloudRegions, ", ")
}

func cloudLoginDefaults(cfg config.Config) (workspace, region string) {
	workspace, region, ok := parseCloudWorkspaceURL(cfg.WorkspaceURL)
	if ok {
		return workspace, region
	}

	if cfg.WorkspaceName != "" {
		workspace = cfg.WorkspaceName
	}
	return workspace, knownCloudRegions[0]
}

func hostBelongsToBaseDomain(workspaceURL, baseDomain string) bool {
	baseDomain = strings.Trim(strings.ToLower(strings.TrimSpace(baseDomain)), ".")
	if baseDomain == "" {
		return false
	}

	parsed, err := url.Parse(formatHostURL(workspaceURL))
	if err != nil {
		return false
	}
	hostname := strings.Trim(strings.ToLower(parsed.Hostname()), ".")
	return hostname == baseDomain || strings.HasSuffix(hostname, "."+baseDomain)
}

// buildCloudWorkspaceURL assembles an Alpacon Cloud workspace URL.
func buildCloudWorkspaceURL(workspace, region string) string {
	return fmt.Sprintf("https://%s.%s.%s", workspace, region, defaultBaseDomain)
}

func validateCloudTargetParts(workspace, region string) error {
	if err := validateDNSLabel("workspace", workspace); err != nil {
		return err
	}
	if err := validateDNSLabel("region", region); err != nil {
		return err
	}
	return nil
}

func validateDNSLabel(name, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s cannot be blank", name)
	}
	if len(value) > 63 {
		return fmt.Errorf("%s must be 63 characters or fewer", name)
	}
	if strings.HasPrefix(value, "-") || strings.HasSuffix(value, "-") {
		return fmt.Errorf("%s cannot start or end with '-'", name)
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
			continue
		}
		return fmt.Errorf("%s must contain only letters, numbers, and hyphens", name)
	}
	return nil
}

// isCloudWorkspaceURL reports true only for the canonical cloud form, so a custom self-hosted URL is never rewritten.
func isCloudWorkspaceURL(workspaceURL string) bool {
	workspace, region, ok := parseCloudWorkspaceURL(workspaceURL)
	if !ok {
		return false
	}
	return strings.EqualFold(formatHostURL(workspaceURL), buildCloudWorkspaceURL(workspace, region))
}

// parseHostURL trims the input, defaults a missing scheme to https, and parses it.
func parseHostURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	return url.Parse(raw)
}

func parseCloudWorkspaceURL(workspaceURL string) (workspace, region string, ok bool) {
	if strings.TrimSpace(workspaceURL) == "" {
		return "", "", false
	}

	parsed, err := parseHostURL(workspaceURL)
	if err != nil {
		return "", "", false
	}

	parts := strings.Split(parsed.Hostname(), ".")
	if len(parts) != 4 {
		return "", "", false
	}
	if !strings.EqualFold(strings.Join(parts[2:], "."), defaultBaseDomain) {
		return "", "", false
	}
	if parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// normalizeCloudFlags trims flag values and reports whether the user supplied
// only blank (whitespace-only) values, which must be rejected rather than
// silently treated as if no flags were passed.
func normalizeCloudFlags(workspace, region string) (w, r string, blank bool) {
	w, r = strings.TrimSpace(workspace), strings.TrimSpace(region)
	blank = (workspace != "" || region != "") && w == "" && r == ""
	return w, r, blank
}

// resolveLoginTarget resolves the non-interactive login target from the HOST
// argument and the cloud --workspace/--region flags, applying all combination
// guards. ok is false when neither a HOST nor cloud flags were supplied, so the
// caller falls back to saved config or interactive prompts.
func resolveLoginTarget(args []string, workspace, region string) (workspaceURL, workspaceName, baseDomain string, ok bool, err error) {
	// Trim cloud flag values so whitespace-only input is rejected up front
	// instead of slipping past validation or building a malformed URL.
	workspace, region, blank := normalizeCloudFlags(workspace, region)
	if blank {
		return "", "", "", false, errors.New("--workspace/--region cannot be blank")
	}
	if len(args) > 0 && (workspace != "" || region != "") {
		return "", "", "", false, errors.New("cannot combine a HOST argument with --workspace/--region. Use a HOST for self-hosted, or --workspace/--region for Alpacon Cloud")
	}

	switch {
	case len(args) > 0:
		// Host mode: self-hosted, plus deprecated Alpacon Cloud direct URLs (backward compat).
		if err := validateHostTarget(args[0]); err != nil {
			return "", "", "", false, err
		}
		workspaceURL = formatHostURL(args[0])
		return workspaceURL, utils.ExtractWorkspaceName(workspaceURL), utils.ExtractBaseDomain(workspaceURL), true, nil
	case workspace != "" || region != "":
		// Alpacon Cloud mode via flags (non-interactive)
		if err := validateCloudFlags(workspace, region); err != nil {
			return "", "", "", false, err
		}
		return buildCloudWorkspaceURL(workspace, region), workspace, defaultBaseDomain, true, nil
	default:
		return "", "", "", false, nil
	}
}

// validateCloudFlags validates the paired --workspace/--region flags.
func validateCloudFlags(workspace, region string) error {
	if workspace == "" && region == "" {
		return nil
	}
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
	return validateCloudTargetParts(workspace, region)
}

// validateHostTarget rejects HOST values beyond scheme, host, and optional port.
func validateHostTarget(host string) error {
	parsed, err := parseHostURL(host)
	if err != nil {
		return unsupportedHostTargetError()
	}
	if parsed.Hostname() == "" {
		return errors.New("host is required (e.g. 'alpacon login alpacon.example.com')")
	}
	// Only http/https are valid; anything else (e.g. ssh://) would otherwise
	// slip past formatHostURL and produce a malformed URL like https://ssh://host.
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return unsupportedHostTargetError()
	}
	// A bare '#' yields an empty Fragment with no ForceFragment flag (net/url has
	// no such field), so check the raw input directly—'#' is never valid in an authority.
	if strings.Contains(host, "#") {
		return unsupportedHostTargetError()
	}
	if parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.TrimSuffix(parsed.Path, "/") != "" {
		return unsupportedHostTargetError()
	}
	return nil
}

func unsupportedHostTargetError() error {
	return errors.New("URL credentials, paths, queries, and fragments are not supported, and the scheme must be http or https. For Alpacon Cloud use 'alpacon login --workspace <name> --region <region>'; for self-hosted pass only the host, with optional http/https scheme and port (e.g. 'alpacon login alpacon.example.com')")
}

// formatHostURL normalizes a host argument into a full URL.
// localhost and 127.0.0.1 default to http://, everything else to https://.
func formatHostURL(host string) string {
	host = strings.TrimSpace(host)
	lowerHost := strings.ToLower(host)
	if strings.HasPrefix(lowerHost, "http://") || strings.HasPrefix(lowerHost, "https://") {
		return strings.TrimSuffix(host, "/")
	}
	scheme := "https"
	// Match loopback by exact hostname so e.g. localhost.example.com stays https.
	if parsed, err := parseHostURL(host); err == nil {
		switch strings.ToLower(parsed.Hostname()) {
		case "localhost", "127.0.0.1", "::1":
			scheme = "http"
		}
	}
	return fmt.Sprintf("%s://%s", scheme, strings.TrimSuffix(host, "/"))
}

// shouldFailOnProfileError reports whether a failed user-profile load should abort
// login. Browser (Auth0) and username/password logins are human logins that always
// have a user profile, so a failed preload signals a real problem and is fatal. Token
// logins were already validated against /api/status/ in LoginAndSaveCredentials, and a
// service token is an application principal that legitimately has no user profile, so a
// failed preload is non-fatal.
func shouldFailOnProfileError(token string) bool {
	return token == ""
}
