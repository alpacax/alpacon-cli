package username

import (
	"fmt"
	"os"

	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var usernameGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Show the username for your account",
	Example: `
	alpacon username get
	alpacon username get --output json
	`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opGet, err, "Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		user, err := iam.GetCurrentUser(alpaconClient)
		if err != nil {
			utils.CliErrorEnvelopeWithExit(opGet, err, "Failed to retrieve the current user: %s.", err)
		}

		if user.Username == "" {
			utils.CliErrorEnvelopeWithExit(opGet, nil, "Username is not set. Run 'alpacon username set <name>' to set it.")
		}

		if utils.OutputFormat == utils.OutputFormatJSON {
			if err := utils.PrintJSONValue(os.Stdout, map[string]string{"username": user.Username}); err != nil {
				utils.CliErrorWithExit("Failed to encode username: %s.", err)
			}
			return
		}

		fmt.Println(user.Username)
	},
}
