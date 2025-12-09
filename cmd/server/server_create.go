package server

import (
	"fmt"
	"strings"

	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var serverCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new server",
	Long: `
	Create a new server with specific configurations. This command allows you to set up a server with a unique name, 
	choose a platform, and define access permissions for different groups. 
	`,
	Example: `alpacon create server`,
	Run: func(cmd *cobra.Command, args []string) {

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		groupList, err := iam.GetGroupList(alpaconClient)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve the group list: %s.", err)
		}

		serverRequest := promptForServer(alpaconClient, groupList)

		response, err := server.CreateServer(alpaconClient, serverRequest)
		if err != nil {
			utils.CliErrorWithExit("Failed to create the new server: %s.", err)
		}

		installServerInfo(response)
	},
}

func promptForServer(ac *client.AlpaconClient, groupList []iam.GroupAttributes) server.ServerRequest {
	var serverRequest server.ServerRequest

	serverRequest.Name = utils.PromptForRequiredInput("Server Name: ")
	serverRequest.Platform = promptForPlatform()

	displayGroups(groupList)

	serverRequest.Groups = selectAndConvertGroups(ac, groupList)

	return serverRequest
}

func promptForPlatform() string {
	for {
		platform := utils.PromptForInput("Platform(debian, rhel): ")
		if strings.ToLower(platform) == "debian" || strings.ToLower(platform) == "rhel" {
			return platform
		}
		fmt.Println("Invalid platform. Please choose 'debian' or 'rhel'. This determines the package manager and system configuration for the server")
	}
}

func displayGroups(groupList []iam.GroupAttributes) {
	fmt.Println("Groups:")
	for i, group := range groupList {
		fmt.Printf("[%d] %s\n", i+1, group.Name)
	}
}

func selectAndConvertGroups(ac *client.AlpaconClient, groupList []iam.GroupAttributes) []string {
	chosenGroups := utils.PromptForRequiredInput("Select groups that are authorized to access this server. (e.g., 1,2):")
	intGroups := utils.SplitAndParseInt(chosenGroups)

	var groupIDs []string

	for _, groupIndex := range intGroups {
		if groupIndex < 1 || groupIndex > len(groupList) {
			utils.CliErrorWithExit("Invalid group index: %d. Please choose a number between 1 and %d from the list above", groupIndex, len(groupList))
		}

		groupID, err := iam.GetGroupIDByName(ac, groupList[groupIndex-1].Name)
		if err != nil {
			utils.CliErrorWithExit("Group '%s' not found. Please verify the group exists and try again", groupList[groupIndex-1].Name)
		}

		groupIDs = append(groupIDs, groupID)
	}

	return groupIDs
}

func installServerInfo(response server.ServerCreatedResponse) {
	fmt.Println()
	utils.PrintHeader("Connecting server to alpacon")
	fmt.Println("We provide two ways to connect your server to alpacon.")
	fmt.Println("Please follow one of the following steps to install the \"alpamon\" agent on your server.")

	printMethod("Simply use our install script:", response.Instruction1)
	printMethod("Or, do it manually (If you've followed the script above, this is not required):", response.Instruction2)
	utils.CliWarning("Please be aware that after leaving this page, you cannot obtain the script again for security.")
}

func printMethod(header, instruction string) {
	fmt.Println(utils.Green(header))
	fmt.Println()
	fmt.Println(instruction + "\n")
}
