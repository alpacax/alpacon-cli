package iam

import (
	"fmt"
	"os"

	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var groupCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new group",
	Long: `
	Create a new group in the Alpacon. 
	This command allows you to add a new group by specifying required group information such as name, servers, and other relevant details.
	`,
	Example: ` 
	alpacon group create
	`,
	Run: func(cmd *cobra.Command, args []string) {

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if alpaconClient.Privileges == "general" {
			utils.CliErrorWithExit("You do not have the permission to create groups.")
		}

		serverList, err := server.GetServerList(alpaconClient)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve the server list: %s.", err)
		}

		groupRequest := promptForGroup(alpaconClient, serverList)

		err = iam.CreateGroup(alpaconClient, groupRequest)
		if err != nil {
			utils.CliErrorWithExit("Failed to create the new group: %s.", err)
		}

		utils.CliSuccess("Group created: %s", groupRequest.Name)
	},
}

func promptForGroup(ac *client.AlpaconClient, serverList []server.ServerAttributes) iam.GroupCreateRequest {
	var groupRequest iam.GroupCreateRequest

	groupRequest.Name = utils.PromptForRequiredInput("Name(required): ")
	groupRequest.DisplayName = utils.PromptForRequiredInput("Display name(required): ")
	groupRequest.Tags = utils.PromptForInput("Tags(optional, Add tags for this group so that people can find easily. Tags should start with \"#\" and be comma-separated.): ")
	groupRequest.Description = utils.PromptForInput("Description(optional): ")

	displayServers(serverList)
	groupRequest.Servers = selectAndConvertServers(ac, serverList)

	groupRequest.IsLdapGroup = utils.PromptForBool("LDAP status: ")

	return groupRequest
}

func displayServers(serverList []server.ServerAttributes) {
	fmt.Fprintln(os.Stderr, "Servers:")
	for i, server := range serverList {
		fmt.Fprintf(os.Stderr, "[%d] %s\n", i+1, server.Name)
	}
}

func selectAndConvertServers(ac *client.AlpaconClient, serverList []server.ServerAttributes) []string {
	chosenServers := utils.PromptForInput("Select servers (e.g., 1,2): ")
	intServers := utils.SplitAndParseInt(chosenServers)

	var serverIDs []string

	for _, serverIndex := range intServers {
		if serverIndex < 1 || serverIndex > len(serverList) {
			utils.CliErrorWithExit("Invalid server index: %d", serverIndex)
		}

		serverID, err := server.GetServerIDByName(ac, serverList[serverIndex-1].Name)
		if err != nil {
			utils.CliErrorWithExit("No server found with the given name")
		}

		serverIDs = append(serverIDs, serverID)
	}

	return serverIDs
}
