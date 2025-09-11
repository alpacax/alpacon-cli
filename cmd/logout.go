package cmd

import (
	"crypto/tls"
	"fmt"
	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/api/auth0"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"net/http"
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
			fmt.Println("You are not logged in.")
			return
		}
		httpClient := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: validConfig.Insecure,
				},
			},
		}
		ac, _ := client.NewAlpaconAPIClient()
		envInfo, _ := auth0.FetchAuthEnv(validConfig.WorkspaceURL, httpClient)

		if envInfo.Auth0.Method == "auth0" {
			err = auth.LogOutWithAuth0(ac, httpClient, validConfig)
			if err != nil {
				utils.CliError("Log out from Alpacon failed: %s.", err)
			}
		} else {
			err := auth.LogOutWithOnPremise(ac)
			if err != nil {
				utils.CliError("Log out from Alpacon failed: %s.", err)
			}
		}
		fmt.Println("Logout succeeded!")
	},
}
