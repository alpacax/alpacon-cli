package token

import (
	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var tokenCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new API token",
	Long: `
	Generates a new API token for accessing the server.
	This command allows you to create a token by specifying options such as token name, expiration, and scopes.
	Personal API tokens cannot run commands or transfer files. Use service tokens for automation that runs commands or transfers files.
	`,
	Example: `
	alpacon token create
	alpacon token create --name ci-token --scopes "server:read"
	alpacon token create --name audit-token --scopes "server:read,role_audit_log:read"
	`,
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		limit, _ := cmd.Flags().GetBool("limit")
		expiresAt, _ := cmd.Flags().GetInt("expiration-in-days")
		scopesStr, _ := cmd.Flags().GetString("scopes")

		var tokenRequest auth.APITokenRequest
		var err error

		if name == "" {
			if tokenRequest, err = promptForToken(); err != nil {
				return err
			}
		} else {
			tokenRequest = auth.APITokenRequest{Name: name}

			if limit && expiresAt > 0 {
				tokenRequest.ExpiresAt = utils.TimeFormat(expiresAt)
			}

			tokenRequest.Scopes = utils.SplitAndTrim(scopesStr, ",")
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		token, err := auth.CreateAPIToken(alpaconClient, tokenRequest)
		if err != nil {
			return utils.NewExitError("Failed to create API token %s.", err)
		}

		utils.CliSuccess("API token created: %s", token)
		utils.CliWarning("This token cannot be retrieved again after you exit.")
		return nil
	}),
}

func init() {
	var name string
	var limit bool
	var expiresAt int

	tokenCreateCmd.Flags().StringVarP(&name, "name", "n", "", "A name to remember the token easily.")
	tokenCreateCmd.Flags().BoolVarP(&limit, "limit", "l", true, "Set to true to apply usage limits.")
	tokenCreateCmd.Flags().IntVar(&expiresAt, "expiration-in-days", 0, "Days until the token expires (0 = the workspace's maximum token lifetime).")
	tokenCreateCmd.Flags().String("scopes", "", `Comma-separated list of scopes (e.g. "server:read,role_audit_log:read"). Omit to get the scopes your roles allow.`)
}

func promptForToken() (auth.APITokenRequest, error) {
	var tokenRequest auth.APITokenRequest
	var err error
	if tokenRequest.Name, err = utils.ReadRequiredInput("Token name: "); err != nil {
		return tokenRequest, err
	}
	if utils.PromptForBool("Set a shorter expiration than the workspace's maximum token lifetime?") {
		tokenRequest.ExpiresAt = utils.TimeFormat(promptForExpiryDays())
	}
	scopes, err := utils.ReadInput("Scopes (comma-separated, default: the scopes your roles allow): ")
	if err != nil {
		return tokenRequest, err
	}
	if list := utils.SplitAndTrim(scopes, ","); len(list) > 0 {
		tokenRequest.Scopes = list
	}
	return tokenRequest, nil
}

func promptForExpiryDays() int {
	for {
		days := utils.PromptForRequiredIntInput("Valid days for the token: ")
		if days > 0 {
			return days
		}
		utils.CliWarning("Enter a number of days greater than 0.")
	}
}
