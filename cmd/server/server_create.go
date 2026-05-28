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
	createMethod       string
	createPlatform     string
	createName         string
	createTokenName    string
	createNewTokenName string
	createJSON         bool
)

var (
	validPlatforms     = []string{"debian", "rhel", "darwin", "windows"}
	validPlatformsList = strings.Join(validPlatforms, ", ")
	validMethods       = []string{"token-install", "ansible"}
	validMethodsList   = strings.Join(validMethods, ", ")
)

var serverCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Register a new server with a registration token",
	Long: `
	Register a new server by selecting a registration method (token-install or ansible)
	and generating an installation guide. Use --method (-m) to choose between
	token-install (default) and ansible.

	token-install (default): the guide includes the Alpamon register command to run on
	your server.

	ansible: the guide produces an ansible-playbook command using the alpacax.alpacon
	collection so you can register one or many servers from a control node.

	Supported platforms: debian, rhel, darwin, windows.

	When --platform and either --token or --new-token are provided, the command runs non-interactively.
	`,
	Example: `
	alpacon server create
	alpacon server create --platform debian --token prod-token
	alpacon server create --platform rhel --token prod-token --name my-server
	alpacon server create --platform darwin --token prod-token
	alpacon server create --method ansible --platform debian --token prod-token
	alpacon server create -m ansible -p windows -t prod-token --json
	`,
	Run: func(cmd *cobra.Command, args []string) {
		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		method := resolveMethod(cmd)
		platform := resolvePlatform(cmd)
		serverName := resolveName(cmd)
		tokenID := resolveTokenID(cmd, alpaconClient)

		switch method {
		case "ansible":
			guide, err := server.GetAnsibleRegistrationGuideJSON(alpaconClient, platform, serverName, tokenID)
			if err != nil {
				utils.CliErrorWithExit("Failed to retrieve the installation guide: %s.", err)
			}
			if createJSON {
				writeJSONGuide(guide)
				return
			}
			displayAnsibleGuideFromJSON(guide)
		default: // token-install
			guide, err := server.GetRegistrationGuideJSON(alpaconClient, platform, serverName, tokenID)
			if err != nil {
				utils.CliErrorWithExit("Failed to retrieve the installation guide: %s.", err)
			}
			if createJSON {
				writeJSONGuide(guide)
				return
			}
			displayGuideFromJSON(guide)
		}
	},
}

func writeJSONGuide(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		utils.CliErrorWithExit("Failed to encode guide as JSON: %s.", err)
	}
}

func init() {
	serverCreateCmd.Flags().StringVarP(&createMethod, "method", "m", "token-install", fmt.Sprintf("registration method: %s", validMethodsList))
	serverCreateCmd.Flags().StringVarP(&createPlatform, "platform", "p", "", fmt.Sprintf("target OS platform: %s", validPlatformsList))
	serverCreateCmd.Flags().StringVarP(&createName, "name", "n", "", "server name (optional; hostname used if not set)")
	serverCreateCmd.Flags().StringVarP(&createTokenName, "token", "t", "", "existing registration token name")
	serverCreateCmd.Flags().StringVar(&createNewTokenName, "new-token", "", "create a new registration token with this name")
	serverCreateCmd.Flags().BoolVar(&createJSON, "json", false, "output the installation guide as structured JSON instead of markdown")
	serverCreateCmd.MarkFlagsMutuallyExclusive("token", "new-token")
}

// resolveMethod returns the registration method from --method flag or interactively.
func resolveMethod(cmd *cobra.Command) string {
	if cmd.Flags().Changed("method") {
		if !slices.Contains(validMethods, createMethod) {
			utils.CliErrorWithExit("Invalid method %q. Valid values: %s.", createMethod, validMethodsList)
		}
		return createMethod
	}
	if !utils.IsInteractiveShell() {
		// non-interactive: fall back to default token-install
		return "token-install"
	}
	return selectMethod()
}

// resolvePlatform returns the platform value from --platform flag or interactively.
func resolvePlatform(cmd *cobra.Command) string {
	if cmd.Flags().Changed("platform") {
		if !slices.Contains(validPlatforms, createPlatform) {
			utils.CliErrorWithExit("Invalid platform %q. Valid values: %s.", createPlatform, validPlatformsList)
		}
		return createPlatform
	}
	return selectPlatform()
}

// resolveName returns the server name from --name flag or interactively.
// When --token or --new-token is set, the caller is scripting: skip the prompt
// and let the server fall back to hostname.
func resolveName(cmd *cobra.Command) string {
	if cmd.Flags().Changed("name") {
		return createName
	}
	if cmd.Flags().Changed("token") || cmd.Flags().Changed("new-token") {
		return ""
	}
	return promptForServerName()
}

// resolveTokenID returns a token UUID from flags or interactively.
// --token: look up existing token by name
// --new-token: create a new token with the given name and show its key
// (mutual exclusion enforced by Cobra; interactive selection used when neither is set)
func resolveTokenID(cmd *cobra.Command, ac *client.AlpaconClient) string {
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

// selectMethod prompts the user to choose a registration method.
// Empty input defaults to token-install to preserve existing muscle memory.
func selectMethod() string {
	fmt.Fprintln(os.Stderr, "Registration method:")
	fmt.Fprintln(os.Stderr, "  [1] Token install")
	fmt.Fprintln(os.Stderr, "  [2] Ansible")
	for {
		input := strings.TrimSpace(utils.PromptForInput("Method [1]: "))
		switch input {
		case "", "1", "token-install":
			return "token-install"
		case "2", "ansible":
			return "ansible"
		}
		utils.CliWarning("Invalid selection. Enter 1 or 2.")
	}
}

func selectPlatform() string {
	if !utils.IsInteractiveShell() {
		utils.CliErrorWithExit("Non-interactive mode requires --platform. Valid values: %s.", validPlatformsList)
	}
	for {
		platform := strings.ToLower(strings.TrimSpace(utils.PromptForInput(fmt.Sprintf("Platform (%s): ", validPlatformsList))))
		if slices.Contains(validPlatforms, platform) {
			return platform
		}
		utils.CliWarning("Invalid platform. Valid values: %s.", validPlatformsList)
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

	if len(tokens) == 0 {
		utils.CliInfo("No existing tokens. Creating a new one.")
		return createNewToken(ac)
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

func printGuideHeader(platformLabel, serverName, alpaconURL string) {
	fmt.Fprintln(os.Stderr)
	utils.PrintHeader("Installation guide")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  Platform : %s\n", platformLabel)
	if serverName != "" {
		fmt.Fprintf(os.Stderr, "  Server   : %s\n", serverName)
	}
	fmt.Fprintf(os.Stderr, "  URL      : %s\n", alpaconURL)
	fmt.Fprintln(os.Stderr)
}

func printGuideVerifyFooter(stepNum int) {
	fmt.Fprintln(os.Stderr, utils.Bold(fmt.Sprintf("Step %d — Verify", stepNum)))
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  Your server will appear in the Servers list within moments.")
}

func displayGuideFromJSON(guide server.RegistrationMethodGuideJsonResponse) {
	printGuideHeader(guide.PlatformLabel, guide.ServerName, guide.AlpaconURL)

	fmt.Fprintln(os.Stderr, utils.Bold("Step 1 — Install Alpamon"))
	fmt.Fprintln(os.Stderr)
	for _, installCmd := range guide.InstallCommands {
		fmt.Fprintln(os.Stderr, installCmd)
	}
	fmt.Fprintln(os.Stderr)

	fmt.Fprintln(os.Stderr, utils.Bold("Step 2 — Register"))
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, guide.RegisterCommand)
	fmt.Fprintln(os.Stderr)

	printGuideVerifyFooter(3)
}

func displayAnsibleGuideFromJSON(guide server.AnsibleGuideJsonResponse) {
	printGuideHeader(guide.PlatformLabel, guide.ServerName, guide.AlpaconURL)

	fmt.Fprintln(os.Stderr, utils.Bold("Step 1 — Install Ansible collection"))
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, guide.CollectionInstall)
	fmt.Fprintln(os.Stderr)

	fmt.Fprintln(os.Stderr, utils.Bold("Step 2 — Configure inventory"))
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Save as inventory.ini:")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, guide.InventorySnippet)
	fmt.Fprintln(os.Stderr)

	fmt.Fprintln(os.Stderr, utils.Bold("Step 3 — Register"))
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, utils.Bold("Option A — Bundled playbook (recommended):"))
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, guide.RunCommandQuick)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, utils.Bold("Option B — Custom playbook:"))
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Save as playbook.yml:")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, guide.PlaybookSnippet)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Then run:")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, guide.RunCommandCustom)
	fmt.Fprintln(os.Stderr)

	printGuideVerifyFooter(4)
}
