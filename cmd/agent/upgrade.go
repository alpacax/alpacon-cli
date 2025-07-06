package agent

import (
	"github.com/alpacax/alpacon-cli/api/agent"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var upgradeAgentCmd = &cobra.Command{
	Use:     "upgrade [SERVER NAME]",
	Short:   "Upgrade server's agent(alpamon)",
	Example: `alpacon agent upgrade myserver`,
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		serverName := args[0]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		err = agent.RequestAgentAction(alpaconClient, serverName, "upgrade")
		if err != nil {
			utils.CliError("Failed to upgrade the agent: %s.", err)
		}

		utils.CliInfo("Agent upgrade request successful. Verify in events.(alpacon events)")
	},
}
