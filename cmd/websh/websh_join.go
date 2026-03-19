package websh

import (
	"github.com/alpacax/alpacon-cli/api/websh"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var webshJoinCmd = &cobra.Command{
	Use:   "join --url URL --password PASSWORD",
	Short: "Join a shared websh session",
	Long:  `Join an existing shared websh session using the provided URL and password.`,
	Example: `  alpacon websh join --url https://myws.us1.alpacon.io/websh/shared/abcd1234?channel=default --password my-session-pass
  alpacon websh join --url https://myws.us1.alpacon.io/websh/shared/abcd1234?channel=default -p my-session-pass`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		url, _ := cmd.Flags().GetString("url")
		password, _ := cmd.Flags().GetString("password")

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		session, err := websh.JoinWebshSession(alpaconClient, url, password)
		if err != nil {
			utils.CliErrorWithExit("Failed to join the session: %s.", err)
		}

		_ = websh.OpenNewTerminal(alpaconClient, session)
	},
}

func init() {
	webshJoinCmd.Flags().String("url", "", "URL of the shared session to join (required)")
	webshJoinCmd.Flags().StringP("password", "p", "", "Password for the shared session (required)")
	_ = webshJoinCmd.MarkFlagRequired("url")
	_ = webshJoinCmd.MarkFlagRequired("password")
}
