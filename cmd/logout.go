package cmd

import (
	"errors"
	"fmt"
	"net/url"

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
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		validConfig, err := config.LoadConfig()
		if err != nil {
			utils.CliInfo("You are not logged in.")
			return nil
		}

		if validConfig.IsMultiWorkspaceMode() {
			utils.CliWarning("This will log you out from all workspaces associated with this account.")
		}

		return reportLogout(runLogout(validConfig))
	}),
}

// runLogout asks the server to end the session as far as it can, then removes
// the local credentials unless another shell has logged in since.
func runLogout(validConfig config.Config) error {
	if !endServerSession(validConfig) {
		utils.CliWarning("The server-side session may still be valid.")
	}
	return config.DeleteConfig(validConfig)
}

// endServerSession reports whether every server call succeeded. A failed call
// only warns, so an offline or expired session can still be logged out.
func endServerSession(validConfig config.Config) bool {
	if _, err := url.Parse(validConfig.WorkspaceURL); err != nil { // every server call below would fail on the same URL
		utils.CliWarning("Skipped the server logout: the workspace URL does not parse: %s.", err)
		return false
	}

	httpClient := httpclient.New(validConfig.Insecure)
	ok := true
	warn := func(format string, err error) {
		utils.CliWarning(format, err)
		ok = false
	}

	// Built first, so it reads the login this command loaded.
	ac, err := client.NewAlpaconAPIClient()
	if err != nil {
		warn("Failed to create Alpacon API client: %s.", err)
		if utils.ClassifyError(err) == utils.ErrorCodeNetwork { // later calls would wait out the same timeout
			return false
		}
	}

	hasAuth0Session := validConfig.AccessToken != "" && validConfig.RefreshToken != ""
	envInfo, err := auth0.FetchAuthEnv(validConfig.WorkspaceURL, httpClient)
	if err != nil {
		warn("Failed to fetch authentication environment: %s.", err)
		if utils.ClassifyError(err) == utils.ErrorCodeNetwork { // later calls would wait out the same timeout
			return false
		}
	}
	isAuth0 := hasAuth0Session // without the auth env the config itself shows the method
	if envInfo != nil {
		isAuth0 = envInfo.Auth0.Method == "auth0"
	}

	if isAuth0 {
		if ac != nil && hasAuth0Session {
			if _, err := ac.SendPostRequest(blacklistURL, nil); err != nil {
				warn("%s", blacklistFailure(err))
			}
		}
		if envInfo != nil && hasAuth0Session { // the revoke needs the Auth0 domain the env names
			if err := auth0.RevokeToken(httpClient, envInfo, validConfig.RefreshToken); err != nil {
				warn("Failed to revoke token: %s.", err)
			}
		}
	} else if ac != nil {
		if err := auth.Logout(ac); err != nil {
			warn("Log out from Alpacon failed: %s.", err)
		}
	}
	return ok
}

func reportLogout(err error) error {
	switch {
	case errors.Is(err, config.ErrLoginChanged):
		utils.CliWarning("Logged out, but kept the config: another shell logged in since this command started.")
	case err != nil:
		return utils.NewExitError("Log out from Alpacon failed: %s.", err)
	default:
		utils.CliSuccess("Logout succeeded!")
	}
	return nil
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
