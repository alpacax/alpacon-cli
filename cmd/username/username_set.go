package username

import (
	"github.com/alpacax/alpacon-cli/api/iam"
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
	alpacon username set jschae
	`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]

		response, err := iam.SetUsername(name)
		if err != nil {
			code, _ := utils.ParseErrorResponse(err)
			if msg, ok := iam.UsernameErrorMessage(code); ok {
				utils.CliErrorWithExit("%s", msg)
			}
			utils.CliErrorWithExit("Failed to set username: %s", err)
		}

		utils.CliSuccess("Username set to %q", response.Username)
	},
}
