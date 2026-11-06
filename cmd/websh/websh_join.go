package websh

import (
	"github.com/alpacax/alpacon-cli/api/websh"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

func newJoinCommand(newClient clientFactory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "join",
		Short: "Join a shared Websh session",
		Long:  `Join an existing shared Websh session using the provided URL and password.`,
		Example: `  alpacon websh join --url https://myws.us1.alpacon.io/websh/shared/abcd1234?channel=default --password my-session-pass
  alpacon websh join --url https://myws.us1.alpacon.io/websh/shared/abcd1234?channel=default -p my-session-pass`,
		Args: cobra.NoArgs,
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			url, _ := cmd.Flags().GetString("url")
			password, _ := cmd.Flags().GetString("password")

			alpaconClient, err := newClient()
			if err != nil {
				return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			session, err := websh.JoinWebshSession(alpaconClient, url, password)
			if err != nil {
				return utils.NewExitError("Failed to join the session: %s.", utils.InteractiveOnly(err, "join a Websh session"))
			}

			if err = websh.OpenSharedTerminal(alpaconClient, session); err != nil {
				return utils.NewExitErrorWithCode(utils.ExitCodeGeneralError, "Websh session ended with error: %s.", err)
			}
			return nil
		}),
	}
	cmd.Flags().String("url", "", "URL of the shared session to join (required)")
	cmd.Flags().StringP("password", "p", "", "Password for the shared session (required)")
	_ = cmd.MarkFlagRequired("url")
	_ = cmd.MarkFlagRequired("password")
	return cmd
}
