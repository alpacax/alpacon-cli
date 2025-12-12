package agent

import (
	"github.com/alpacax/alpacon-cli/api/agent"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var shutdownAgentCmd = &cobra.Command{
	Use:     "shutdown [SERVER NAME]",
	Short:   "Shutdown server's agent(alpamon)",
	Example: `alpacon agent shutdown myserver`,
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		serverName := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = agent.RequestAgentAction(alpaconClient, serverName, "shutdown")
		if err != nil {
			utils.CliErrorWithExit("Failed to shutdown the agent: %s.", err)
		}

		utils.CliInfo("Agent shutdown request successful. Verify in events.(alpacon events)")
	},
}
