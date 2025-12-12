package token

import (
	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var tokenCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new api token",
	Long: `
	Generates a new API token for accessing the server. 
	This command allows you to create a token by specifying options such as token name, expiration, and limits
	`,
	Example: `alpacon create token`,
	Run: func(cmd *cobra.Command, args []string) {
		name, _ := cmd.Flags().GetString("name")
		limit, _ := cmd.Flags().GetBool("limit")
		expiresAt, _ := cmd.Flags().GetInt("expiration-in-days")

		var tokenRequest auth.APITokenRequest
		var err error

		if name == "" || (limit && expiresAt == 0) {
			tokenRequest, err = promptForToken()
			if err != nil {
				utils.CliErrorWithExit("During token input: %v. Check your input and try again.", err)
			}
		} else {
			tokenRequest = auth.APITokenRequest{Name: name}

			if limit && expiresAt > 0 {
				tokenRequest.ExpiresAt = utils.TimeFormat(expiresAt)
			}
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		token, err := auth.CreateAPIToken(alpaconClient, tokenRequest)
		if err != nil {
			utils.CliErrorWithExit("Failed to create API token %s.", err)
		}

		utils.CliInfo("API Token Created: `%s`", token)
		utils.CliWarning("This token cannot be retrieved again after you exit.")
	},
}

func init() {
	var name string
	var limit bool
	var expiresAt int

	tokenCreateCmd.Flags().StringVarP(&name, "name", "n", "", "A name to remember the token easily.")
	tokenCreateCmd.Flags().BoolVarP(&limit, "limit", "l", true, "Set to true to apply usage limits.")
	tokenCreateCmd.Flags().IntVar(&expiresAt, "expiration-in-days", 0, "This token can be used by the specified time. (in days)")
}

func promptForToken() (auth.APITokenRequest, error) {
	var tokenRequest auth.APITokenRequest
	tokenRequest.Name = utils.PromptForRequiredInput("Token name:")
	if utils.PromptForBool("Set expiration for token?: ") {
		tokenRequest.ExpiresAt = utils.TimeFormat(utils.PromptForIntInput("Valid days for the token (default: 30): ", 30))
	} else {
		tokenRequest.ExpiresAt = nil
	}
	return tokenRequest, nil
}
