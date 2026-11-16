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
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if err := alpaconClient.LoadCurrentUser(); err != nil {
			return utils.NewExitError("Failed to load current user: %s", err)
		}

		if alpaconClient.Privileges == "general" {
			return utils.NewExitError("You do not have the permission to create groups.")
		}

		serverList, err := server.GetServerList(alpaconClient)
		if err != nil {
			return utils.NewExitError("Failed to retrieve the server list: %s", err)
		}

		groupRequest, err := promptForGroup(alpaconClient, serverList)
		if err != nil {
			return err
		}

		err = iam.CreateGroup(alpaconClient, groupRequest)
		if err != nil {
			return utils.NewExitError("Failed to create the new group: %s", err)
		}

		utils.CliSuccess("Group created: %s", groupRequest.Name)
		return nil
	}),
}

func promptForGroup(ac *client.AlpaconClient, serverList []server.ServerAttributes) (iam.GroupCreateRequest, error) {
	var groupRequest iam.GroupCreateRequest
	var err error

	if groupRequest.Name, err = utils.ReadRequiredInput("Name(required): "); err != nil {
		return groupRequest, err
	}
	if groupRequest.DisplayName, err = utils.ReadRequiredInput("Display name(required): "); err != nil {
		return groupRequest, err
	}
	if groupRequest.Tags, err = utils.ReadInput("Tags(optional, Add tags for this group so that people can find easily. Tags should start with \"#\" and be comma-separated.): "); err != nil {
		return groupRequest, err
	}
	if groupRequest.Description, err = utils.ReadInput("Description(optional): "); err != nil {
		return groupRequest, err
	}

	displayServers(serverList)
	servers, err := selectAndConvertServers(ac, serverList)
	if err != nil {
		return groupRequest, err
	}
	groupRequest.Servers = servers

	groupRequest.IsLdapGroup = utils.PromptForBool("LDAP status: ")

	return groupRequest, nil
}

func displayServers(serverList []server.ServerAttributes) {
	fmt.Fprintln(os.Stderr, "Servers:")
	for i, server := range serverList {
		fmt.Fprintf(os.Stderr, "[%d] %s\n", i+1, server.Name)
	}
}

func selectAndConvertServers(ac *client.AlpaconClient, serverList []server.ServerAttributes) ([]string, error) {
	chosenServers, err := utils.ReadInput("Select servers (e.g., 1,2): ")
	if err != nil {
		return nil, err
	}
	intServers, err := utils.ParseIntList(chosenServers)
	if err != nil {
		return nil, err
	}

	var serverIDs []string

	for _, serverIndex := range intServers {
		if serverIndex < 1 || serverIndex > len(serverList) {
			return nil, utils.NewExitError("Invalid server index: %d", serverIndex)
		}

		serverID, err := server.GetServerIDByName(ac, serverList[serverIndex-1].Name)
		if err != nil {
			return nil, utils.NewExitError("No server found with the given name")
		}

		serverIDs = append(serverIDs, serverID)
	}

	return serverIDs, nil
}
