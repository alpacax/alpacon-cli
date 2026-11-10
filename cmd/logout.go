package cmd

import (
	"errors"
	"fmt"

	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/api/auth0"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/httpclient"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

const (
	blacklistURL = "/api/auth0/token/blacklist/"
)

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Log out of Alpacon",
	Long:  "Log out of Alpacon. This command removes your authentication credentials stored locally on your system.",
	Example: `
	alpacon logout
	`,
	Run: func(cmd *cobra.Command, args []string) {
		validConfig, err := config.LoadConfig()
		if err != nil {
			utils.CliInfoWithExit("You are not logged in.")
		}

		if validConfig.IsMultiWorkspaceMode() {
			utils.CliWarning("This will log you out from all workspaces associated with this account.")
		}

		reportLogout(runLogout(validConfig))
	},
}

// runLogout asks the server to end the session as far as it can, then always
// removes the local credentials.
func runLogout(validConfig config.Config) error {
	if !endServerSession(validConfig) {
		utils.CliWarning("The server-side session may still be valid.")
	}
	return config.DeleteConfig(validConfig)
}

// endServerSession reports whether every server call succeeded. A failed call
// only warns, so an offline or expired session can still be logged out.
func endServerSession(validConfig config.Config) bool {
	httpClient := httpclient.New(validConfig.Insecure)
	ok := true
	warn := func(format string, err error) {
		utils.CliWarning(format, err)
		ok = false
	}

	// Built before any request, so it reads the same login another shell may soon replace.
	ac, err := client.NewAlpaconAPIClient()
	if err != nil {
		warn("Failed to create Alpacon API client: %s.", err)
	}

	// Without the auth env lookup the config itself shows the method.
	isAuth0 := validConfig.AccessToken != "" && validConfig.RefreshToken != ""
	if envInfo, err := auth0.FetchAuthEnv(validConfig.WorkspaceURL, httpClient); err != nil {
		warn("Failed to fetch authentication environment: %s.", err)
	} else {
		isAuth0 = envInfo.Auth0.Method == "auth0"
	}

	if isAuth0 {
		if ac != nil && validConfig.AccessToken != "" && validConfig.RefreshToken != "" {
			if _, err := ac.SendPostRequest(blacklistURL, nil); err != nil {
				warn("%s", blacklistFailure(err))
			}
		}
		if err := auth0.Logout(httpClient, validConfig); err != nil {
			warn("Log out from Alpacon failed: %s.", err)
		}
	} else if ac != nil {
		if err := auth.Logout(ac); err != nil {
			warn("Log out from Alpacon failed: %s.", err)
		}
	}
	return ok
}

func reportLogout(err error) {
	switch {
	case errors.Is(err, config.ErrLoginChanged):
		utils.CliWarning("Logged out, but kept the config: another shell logged in since this command started.")
	case err != nil:
		utils.CliErrorWithExit("Log out from Alpacon failed: %s.", err)
	default:
		utils.CliSuccess("Logout succeeded!")
	}
}

// blacklistFailure words a failed token blacklist. A refusal carrying the
// server's hint keeps it, printed under the error, in place of the advice to
// try again later.
func blacklistFailure(err error) error {
	if utils.RefusalHint(err) != "" {
		return utils.Reword(err, fmt.Sprintf("Failed to set black list: %s.", err))
	}
	return utils.Reword(err, "Failed to set black list. Please try again later.")
}
