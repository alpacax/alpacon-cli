package username

import (
	"encoding/json"
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
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		user, err := iam.GetCurrentUser(alpaconClient)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve the current user: %s.", err)
		}

		if user.Username == "" {
			fmt.Fprintf(os.Stderr, "%s: username is not set.\n", utils.Red("Error"))
			fmt.Fprintln(os.Stderr, "Run 'alpacon username set <name>' to set it.")
			os.Exit(1)
		}

		if utils.OutputFormat == utils.OutputFormatJSON {
			out, err := json.Marshal(map[string]string{"username": user.Username})
			if err != nil {
				utils.CliErrorWithExit("Failed to encode username: %s.", err)
			}
			utils.PrintJson(out)
			return
		}

		fmt.Println(user.Username)
	},
}
