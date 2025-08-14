package cmd

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/api/auth0"
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
	alpacon login

	alpacon login [WORKSPACE_URL] -u [USERNAME] -p [PASSWORD]
	alpacon login [WORKSPACE_URL]
	
	# Include http if using localhost.
	alpacon login http://localhost:8000
	
	# Login via API Token
	alpacon login [WORKSPACE_URL] -t [TOKEN_KEY]

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
			workspaceURL = utils.PromptForRequiredInput("workspaceURL: ")
		}

		httpClient := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: insecure,
				},
			},
		}

		// Validate workspaceURL
		workspaceURL, err := validateAndFormatWorkspaceURL(workspaceURL, httpClient)
		if err != nil {
			utils.CliError(err.Error())
		}

		// Check login method
		envInfo, err := auth0.FetchAuthEnv(workspaceURL, httpClient)
		if err != nil {
			if strings.Contains(err.Error(), "404") {
				// envInfo = &auth0.AuthEnvResponse{Method: "legacy"}
				envInfo = &auth0.AuthEnvResponse{
					Auth0: auth0.Auth0Config{
						Method: "legacy",
					},
				}
			} else {
				utils.CliError("Failed to patch environment variables from workspace. %v", err)
			}
		}

		username, _ := cmd.Flags().GetString("username")
		password, _ := cmd.Flags().GetString("password")
		token, _ := cmd.Flags().GetString("token")

		fmt.Printf("Logging in to %s\n", workspaceURL)
		workspaceName := utils.ExtractWorkspaceName(workspaceURL)

		if envInfo.Auth0.Method == "auth0" && token == "" {
			deviceCode, err := auth0.RequestDeviceCode(workspaceURL, httpClient, envInfo)
			if err != nil {
				utils.CliError("Device code request failed. %v", err)
			}

			highlight := "\033[1;34m" // blue + bold
			reset := "\033[0m"

			fmt.Println("\n==================== AUTHENTICATION REQUIRED ====================")
			fmt.Println("\nPlease authenticate by visiting the following URL:")
			fmt.Printf("%s%s%s\n\n", highlight, deviceCode.VerificationURIComplete, reset)
			fmt.Print("===============================================================\n\n")

			tokenRes, err := auth0.PollForToken(deviceCode, envInfo)
			if err != nil {
				utils.CliError(err.Error())
			}

			err = config.CreateConfig(workspaceURL, workspaceName, "", "", tokenRes.AccessToken, tokenRes.RefreshToken, tokenRes.ExpiresIn, insecure)
			if err != nil {
				utils.CliError("Failed to save config: %v", err)
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
				utils.CliError("Login failed: %v. Please verify your username, password, and workspace URL are correct. If using a token, ensure it's valid and has not expired", err)
			}

		}
		_, err = client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
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

		// ToDo: modify below code after region decision in portal (multiple region)
		if domain == "alpacon.io" {
			domain = "ap1." + domain
		}
		if domain != "" && workspace != "" {
			workspaceURL = fmt.Sprintf("%s://%s.%s", protocol, workspace, domain)
		}
	}

	resp, err := httpClient.Get(workspaceURL)
	if err != nil || resp.StatusCode >= 400 {
		return "", fmt.Errorf("workspace URL '%s' is unreachable or invalid. Please check the URL and your internet connection", workspaceURL)
	}
	defer resp.Body.Close()

	return workspaceURL, nil
}
