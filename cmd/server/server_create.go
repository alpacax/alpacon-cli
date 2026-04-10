package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var (
	createPlatform     string
	createName         string
	createTokenName    string
	createNewTokenName string
	createJSON         bool
)

var serverCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Register a new server with a registration token",
	Long: `
	Register a new server by selecting a registration token and generating an installation guide.
	The guide includes the Alpamon register command to run on your server.

	When --platform and either --token or --new-token are provided, the command runs non-interactively.
	`,
	Example: `
	alpacon server create
	alpacon server create --platform debian --token prod-token
	alpacon server create --platform rhel --token prod-token --name my-server
	`,
	Run: func(cmd *cobra.Command, args []string) {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		platform := resolvePlatform(cmd)
		serverName := resolveName(cmd)
		tokenID := resolveTokenID(cmd, alpaconClient)

		if createJSON {
			guide, err := server.GetRegistrationGuideJSON(alpaconClient, platform, serverName, tokenID)
			if err != nil {
				utils.CliErrorWithExit("Failed to retrieve the installation guide: %s.", err)
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(guide); err != nil {
				utils.CliErrorWithExit("Failed to encode guide as JSON: %s.", err)
			}
			return
		}

		guide, err := server.GetRegistrationGuide(alpaconClient, platform, serverName, tokenID)
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve the installation guide: %s.", err)
		}

		displayGuide(guide)
	},
}

func init() {
	serverCreateCmd.Flags().StringVarP(&createPlatform, "platform", "p", "", "target OS platform: debian, rhel, darwin")
	serverCreateCmd.Flags().StringVarP(&createName, "name", "n", "", "server name (optional; hostname used if not set)")
	serverCreateCmd.Flags().StringVarP(&createTokenName, "token", "t", "", "existing registration token name")
	serverCreateCmd.Flags().StringVar(&createNewTokenName, "new-token", "", "create a new registration token with this name")
	serverCreateCmd.Flags().BoolVar(&createJSON, "json", false, "output the installation guide as structured JSON instead of markdown")
}

// resolvePlatform returns the platform value from --platform flag or interactively.
func resolvePlatform(cmd *cobra.Command) string {
	if cmd.Flags().Changed("platform") {
		if !slices.Contains(validPlatforms, createPlatform) {
			utils.CliErrorWithExit("Invalid platform %q. Valid values: debian, rhel, darwin.", createPlatform)
		}
		return createPlatform
	}
	return selectPlatform()
}

// resolveName returns the server name from --name flag or interactively.
func resolveName(cmd *cobra.Command) string {
	if cmd.Flags().Changed("name") {
		return createName
	}
	return promptForServerName()
}

// resolveTokenID returns a token UUID from flags or interactively.
// --token: look up existing token by name
// --new-token: create a new token with the given name and show its key
// (mutually exclusive; interactive selection used when neither is set)
func resolveTokenID(cmd *cobra.Command, ac *client.AlpaconClient) string {
	if cmd.Flags().Changed("token") && cmd.Flags().Changed("new-token") {
		utils.CliErrorWithExit("--token and --new-token are mutually exclusive.")
	}
	if cmd.Flags().Changed("token") {
		token, err := server.GetRegistrationTokenByName(ac, createTokenName)
		if err != nil {
			if errors.Is(err, server.ErrRegistrationTokenNotFound) {
				utils.CliErrorWithExit("Registration token %q not found.", createTokenName)
			}
			utils.CliErrorWithExit("Failed to retrieve registration token %q: %s.", createTokenName, err)
		}
		return token.ID
	}
	if cmd.Flags().Changed("new-token") {
		return createTokenAndWarn(ac, createNewTokenName)
	}
	return selectOrCreateToken(ac)
}

var validPlatforms = []string{"debian", "rhel", "darwin"}

func selectPlatform() string {
	if !utils.IsInteractiveShell() {
		utils.CliErrorWithExit("Non-interactive mode requires --platform. Valid values: debian, rhel, darwin.")
	}
	for {
		platform := strings.ToLower(strings.TrimSpace(utils.PromptForInput("Platform (debian, rhel, darwin): ")))
		if slices.Contains(validPlatforms, platform) {
			return platform
		}
		utils.CliWarning("Invalid platform. Valid values: debian, rhel, darwin.")
	}
}

func promptForServerName() string {
	if !utils.IsInteractiveShell() {
		return ""
	}
	fmt.Fprintln(os.Stderr, "Server name (optional—hostname will be used if not specified):")
	return strings.TrimSpace(utils.PromptForInput("Server Name: "))
}

// selectOrCreateToken lists existing registration tokens and lets the user pick one
// or create a new one. Returns the selected or newly created token UUID.
func selectOrCreateToken(ac *client.AlpaconClient) string {
	if !utils.IsInteractiveShell() {
		utils.CliErrorWithExit("Non-interactive mode requires --token or --new-token.")
	}
	tokens, err := server.ListRegistrationTokens(ac)
	if err != nil {
		utils.CliErrorWithExit("Failed to retrieve registration tokens: %s.", err)
	}

	fmt.Fprintln(os.Stderr, "Select a registration token:")
	for i, t := range tokens {
		fmt.Fprintf(os.Stderr, "  [%d] %s\n", i+1, t.Name)
	}
	fmt.Fprintln(os.Stderr, "  [+] Create new token")

	for {
		input := strings.TrimSpace(utils.PromptForRequiredInput("Token: "))

		if input == "+" {
			return createNewToken(ac)
		}

		idx, err := strconv.Atoi(input)
		if err == nil && idx >= 1 && idx <= len(tokens) {
			return tokens[idx-1].ID
		}
		utils.CliWarning("Invalid selection. Enter a number from the list or '+' to create a new token.")
	}
}

func createNewToken(ac *client.AlpaconClient) string {
	name := utils.PromptForRequiredInput("Token name: ")
	return createTokenAndWarn(ac, name)
}

func createTokenAndWarn(ac *client.AlpaconClient, name string) string {
	response, err := server.CreateRegistrationToken(ac, server.RegistrationTokenRequest{Name: name})
	if err != nil {
		utils.CliErrorWithExit("Failed to create the registration token: %s.", err)
	}
	utils.CliWarning("New token created. Save the key now—it will not be shown again: %s", utils.Green(response.Key))
	return response.ID
}

func displayGuide(content string) {
	fmt.Fprintln(os.Stderr)
	utils.PrintHeader("Installation guide")
	fmt.Fprintln(os.Stderr, content)
}
