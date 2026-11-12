package username

import (
	"fmt"

	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var usernameSetCmd = &cobra.Command{
	Use:   "set NAME",
	Short: "Set the username for your account",
	Long: `
	Set the username used for server access services like websh and webftp.
	The username can be set once; it cannot be changed here afterward.
	`,
	Example: `
	alpacon username set alice
	`,
	Args: cobra.ExactArgs(1),
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		name := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		response, err := iam.SetUsername(alpaconClient, name)
		if err != nil {
			return utils.NewExitError("%s", utils.Reword(err, setUsernameErrorText(err)))
		}

		utils.CliSuccess(iam.UsernameSetSuccessFmt, response.Username)
		return nil
	}),
}

func setUsernameErrorText(err error) string {
	code, _ := utils.ParseErrorResponse(err)
	// A server hint is the guidance when the refusal carries one, and the error
	// output prints it under this line.
	if msg, ok := iam.UsernameErrorMessage(code); ok && utils.RefusalHint(err) == "" {
		return msg
	}
	return fmt.Sprintf("Failed to set username: %s", err)
}
