package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var (
	validPlatforms     = []string{"debian", "rhel", "suse", "darwin", "windows"}
	validPlatformsList = strings.Join(validPlatforms, ", ")
	// The server's ansible method does not support suse.
	ansiblePlatforms     = slices.DeleteFunc(slices.Clone(validPlatforms), func(p string) bool { return p == "suse" })
	ansiblePlatformsList = strings.Join(ansiblePlatforms, ", ")
	validMethods         = []string{"token-install", "ansible"}
	validMethodsList     = strings.Join(validMethods, ", ")
)

var serverCreateCmd = newServerCreateCmd(client.NewAlpaconAPIClient, utils.Stdio)

func newServerCreateCmd(newClient clientFactory, ios *utils.IOStreams) *cobra.Command {
	var createJSON bool
	cmd := &cobra.Command{
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

	Supported platforms: debian, rhel, suse (openSUSE/SLES), darwin, windows.
	The ansible method does not support suse.

	When --platform and either --token or --new-token are provided, the command runs non-interactively.
	`,
		Example: `
	alpacon server create
	alpacon server create --platform debian --token prod-token
	alpacon server create --platform rhel --token prod-token --name my-server
	alpacon server create --platform suse --token prod-token
	alpacon server create --platform darwin --token prod-token
	alpacon server create --method ansible --platform debian --token prod-token
	alpacon server create -m ansible -p windows -t prod-token --json
	`,
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			if refusal := flagRefusal(cmd, utils.IsInteractiveShell()); refusal != "" {
				return utils.NewUsageExitError("%s", refusal)
			}

			alpaconClient, err := newClient()
			if err != nil {
				return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			method, err := resolveMethod(ios, cmd)
			if err != nil {
				return err
			}
			platform, err := resolvePlatform(ios, cmd, method)
			if err != nil {
				return err
			}
			serverName, err := resolveName(ios, cmd)
			if err != nil {
				return err
			}
			tokenID, err := resolveTokenID(ios, cmd, alpaconClient)
			if err != nil {
				return err
			}

			switch method {
			case "ansible":
				guide, err := server.GetAnsibleRegistrationGuideJSON(alpaconClient, platform, serverName, tokenID)
				if err != nil {
					return utils.NewExitError("Failed to retrieve the installation guide: %s.", err)
				}
				if createJSON {
					return writeJSONGuide(ios, guide)
				}
				displayAnsibleGuideFromJSON(ios, guide)
			case "token-install":
				guide, err := server.GetRegistrationGuideJSON(alpaconClient, platform, serverName, tokenID)
				if err != nil {
					return utils.NewExitError("Failed to retrieve the installation guide: %s.", err)
				}
				if createJSON {
					return writeJSONGuide(ios, guide)
				}
				displayGuideFromJSON(ios, guide)
			default:
				return utils.NewExitError("Unknown registration method %q. Valid values: %s.", method, validMethodsList)
			}
			return nil
		}),
	}
	cmd.Flags().StringP("method", "m", "token-install", fmt.Sprintf("registration method: %s", validMethodsList))
	cmd.Flags().StringP("platform", "p", "", fmt.Sprintf("target OS platform: %s (suse is not supported with --method ansible)", validPlatformsList))
	cmd.Flags().StringP("name", "n", "", "server name (optional; hostname used if not set)")
	cmd.Flags().StringP("token", "t", "", "existing registration token name")
	cmd.Flags().String("new-token", "", "create a new registration token with this name")
	cmd.Flags().BoolVar(&createJSON, "json", false, "output the installation guide as structured JSON instead of markdown")
	cmd.MarkFlagsMutuallyExclusive("token", "new-token")
	return cmd
}

func writeJSONGuide(ios *utils.IOStreams, v any) error {
	enc := json.NewEncoder(ios.Out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return utils.NewExitError("Failed to encode guide as JSON: %s.", err)
	}
	return nil
}

// resolveMethod returns --method, token-install in a non-interactive shell, or the
// user's choice.
func resolveMethod(ios *utils.IOStreams, cmd *cobra.Command) (string, error) {
	if cmd.Flags().Changed("method") {
		method, _ := cmd.Flags().GetString("method")
		if refusal := methodRefusal(method); refusal != "" {
			return "", utils.NewUsageExitError("%s", refusal)
		}
		return method, nil
	}
	if !utils.IsInteractiveShell() {
		return "token-install", nil
	}
	return selectMethod(ios)
}

func resolvePlatform(ios *utils.IOStreams, cmd *cobra.Command, method string) (string, error) {
	if cmd.Flags().Changed("platform") {
		platform, _ := cmd.Flags().GetString("platform")
		if refusal := platformRefusal(method, platform); refusal != "" {
			return "", utils.NewUsageExitError("%s", refusal)
		}
		return platform, nil
	}
	return selectPlatform(ios, method)
}

// flagRefusal checks --method and --platform without prompting, so a bad value is refused
// before NewAlpaconAPIClient, which may refresh an expired token over the network.
func flagRefusal(cmd *cobra.Command, interactive bool) string {
	method := ""
	if cmd.Flags().Changed("method") {
		method, _ = cmd.Flags().GetString("method")
		if refusal := methodRefusal(method); refusal != "" {
			return refusal
		}
	} else if !interactive {
		method = "token-install"
	}
	if !cmd.Flags().Changed("platform") {
		return ""
	}
	platform, _ := cmd.Flags().GetString("platform")
	return platformRefusal(method, platform)
}

func methodRefusal(method string) string {
	if slices.Contains(validMethods, method) {
		return ""
	}
	return fmt.Sprintf("Invalid method %q. Valid values: %s.", method, validMethodsList)
}

func platformRefusal(method, platform string) string {
	allowed, list := platformsForMethod(method)
	switch {
	case slices.Contains(allowed, platform):
		return ""
	case slices.Contains(validPlatforms, platform):
		return fmt.Sprintf("Platform %q is not supported with the %s method. Valid values: %s.", platform, method, list)
	default:
		return fmt.Sprintf("Invalid platform %q. Valid values: %s.", platform, list)
	}
}

func platformsForMethod(method string) ([]string, string) {
	if method == "ansible" {
		return ansiblePlatforms, ansiblePlatformsList
	}
	return validPlatforms, validPlatformsList
}

// resolveName returns --name or prompts for one. With --token or --new-token the
// caller is scripting, so it returns empty and the server falls back to the hostname.
func resolveName(ios *utils.IOStreams, cmd *cobra.Command) (string, error) {
	if cmd.Flags().Changed("name") {
		name, _ := cmd.Flags().GetString("name")
		return name, nil
	}
	if cmd.Flags().Changed("token") || cmd.Flags().Changed("new-token") {
		return "", nil
	}
	return promptForServerName(ios)
}

// resolveTokenID returns the UUID of the --token token, of one created for
// --new-token, or of the user's interactive choice.
func resolveTokenID(ios *utils.IOStreams, cmd *cobra.Command, ac *client.AlpaconClient) (string, error) {
	if cmd.Flags().Changed("token") {
		createTokenName, _ := cmd.Flags().GetString("token")
		token, err := server.GetRegistrationTokenByName(ac, createTokenName)
		if err != nil {
			if errors.Is(err, server.ErrRegistrationTokenNotFound) {
				return "", utils.NewExitError("Registration token %q not found.", createTokenName)
			}
			return "", utils.NewExitError("Failed to retrieve registration token %q: %s.", createTokenName, err)
		}
		return token.ID, nil
	}
	if cmd.Flags().Changed("new-token") {
		newTokenName, _ := cmd.Flags().GetString("new-token")
		return createTokenAndWarn(ios, ac, newTokenName)
	}
	return selectOrCreateToken(ios, ac)
}

// selectMethod prompts the user to choose a registration method.
// Empty input defaults to token-install to preserve existing muscle memory.
func selectMethod(ios *utils.IOStreams) (string, error) {
	_, _ = fmt.Fprintln(ios.ErrOut, "Registration method:")
	_, _ = fmt.Fprintln(ios.ErrOut, "  [1] Token install")
	_, _ = fmt.Fprintln(ios.ErrOut, "  [2] Ansible")
	for {
		input, err := utils.ReadInput("Method [1]: ")
		if err != nil {
			return "", err
		}
		switch input {
		case "", "1", "token-install":
			return "token-install", nil
		case "2", "ansible":
			return "ansible", nil
		}
		ios.Warning("Invalid selection. Enter 1 or 2.")
	}
}

func selectPlatform(ios *utils.IOStreams, method string) (string, error) {
	allowed, list := platformsForMethod(method)
	if !utils.IsInteractiveShell() {
		return "", utils.NewUsageExitError("Non-interactive mode requires --platform. Valid values: %s.", list)
	}
	return promptPlatform(ios, allowed, list, utils.ReadInput)
}

func promptPlatform(ios *utils.IOStreams, allowed []string, list string, prompt func(string) (string, error)) (string, error) {
	for {
		input, err := prompt(fmt.Sprintf("Platform (%s): ", list))
		if err != nil {
			return "", err
		}
		platform := strings.ToLower(strings.TrimSpace(input))
		if slices.Contains(allowed, platform) {
			return platform, nil
		}
		ios.Warning("Invalid platform. Valid values: %s.", list)
	}
}

func promptForServerName(ios *utils.IOStreams) (string, error) {
	if !utils.IsInteractiveShell() {
		return "", nil
	}
	_, _ = fmt.Fprintln(ios.ErrOut, "Server name (optional—hostname will be used if not specified):")
	return utils.ReadInput("Server Name: ")
}

// selectOrCreateToken lists existing registration tokens and lets the user pick one
// or create a new one. Returns the selected or newly created token UUID.
func selectOrCreateToken(ios *utils.IOStreams, ac *client.AlpaconClient) (string, error) {
	if !utils.IsInteractiveShell() {
		return "", utils.NewUsageExitError("Non-interactive mode requires --token or --new-token.")
	}
	tokens, err := server.ListRegistrationTokens(ac)
	if err != nil {
		return "", utils.NewExitError("Failed to retrieve registration tokens: %s.", err)
	}

	if len(tokens) == 0 {
		ios.Info("No existing tokens. Creating a new one.")
		return createNewToken(ios, ac)
	}

	printTokenChoices(ios.ErrOut, tokens)

	for {
		input, err := utils.ReadRequiredInput("Token: ")
		if err != nil {
			return "", err
		}

		if input == "+" {
			return createNewToken(ios, ac)
		}

		idx, err := strconv.Atoi(input)
		if err == nil && idx >= 1 && idx <= len(tokens) {
			return tokens[idx-1].ID, nil
		}
		ios.Warning("Invalid selection. Enter a number from the list or '+' to create a new token.")
	}
}

// Token names are set by whoever created them, and a newline in one forges a
// numbered choice the operator can pick.
func printTokenChoices(w io.Writer, tokens []server.RegistrationTokenDetails) {
	_, _ = fmt.Fprintln(w, "Select a registration token:")
	for i, t := range tokens {
		_, _ = fmt.Fprintf(w, "  [%d] %s\n", i+1, utils.SanitizeTerminalText(t.Name))
	}
	_, _ = fmt.Fprintln(w, "  [+] Create new token")
}

func createNewToken(ios *utils.IOStreams, ac *client.AlpaconClient) (string, error) {
	name, err := utils.ReadRequiredInput("Token name: ")
	if err != nil {
		return "", err
	}
	return createTokenAndWarn(ios, ac, name)
}

func createTokenAndWarn(ios *utils.IOStreams, ac *client.AlpaconClient, name string) (string, error) {
	response, err := server.CreateRegistrationToken(ac, server.RegistrationTokenRequest{Name: name})
	if err != nil {
		return "", utils.NewExitError("Failed to create the registration token: %s.", err)
	}
	warnTokenKey(ios, "New token created. Save the key now—it will not be shown again", response.Key)
	return response.ID, nil
}

func printGuideHeader(ios *utils.IOStreams, platformLabel, serverName, alpaconURL string) {
	_, _ = fmt.Fprintln(ios.ErrOut)
	ios.PrintHeader("Installation guide")
	_, _ = fmt.Fprintln(ios.ErrOut)
	printGuideFields(ios.ErrOut, platformLabel, serverName, alpaconURL)
	_, _ = fmt.Fprintln(ios.ErrOut)
}

// Every value here is echoed from the guide response; a newline in one forges a field line.
func printGuideFields(w io.Writer, platformLabel, serverName, alpaconURL string) {
	_, _ = fmt.Fprintf(w, "  Platform : %s\n", utils.SanitizeTerminalText(platformLabel))
	if serverName != "" {
		_, _ = fmt.Fprintf(w, "  Server   : %s\n", utils.SanitizeTerminalText(serverName))
	}
	_, _ = fmt.Fprintf(w, "  URL      : %s\n", utils.SanitizeTerminalText(alpaconURL))
}

func printGuideVerifyFooter(ios *utils.IOStreams, stepNum int) {
	_, _ = fmt.Fprintln(ios.ErrOut, ios.Bold(fmt.Sprintf("Step %d — Verify", stepNum)))
	_, _ = fmt.Fprintln(ios.ErrOut)
	_, _ = fmt.Fprintln(ios.ErrOut, "  Your server will appear in the Servers list within moments.")
}

// printGuideCommand warns rather than withholds: the operator decides, and an
// automation reading this guide keeps working. The warning goes above the
// value, so a guide with several altered fields says which ones.
func printGuideCommand(ios *utils.IOStreams, w io.Writer, s string) {
	clean, altered := utils.SanitizeTerminalBlock(s)
	if altered {
		ios.WarnTerminalTextAltered(w, "  ")
	}
	_, _ = fmt.Fprintln(w, clean)
}

func displayGuideFromJSON(ios *utils.IOStreams, guide server.RegistrationMethodGuideJsonResponse) {
	printGuideHeader(ios, guide.PlatformLabel, guide.ServerName, guide.AlpaconURL)

	_, _ = fmt.Fprintln(ios.ErrOut, ios.Bold("Step 1 — Install Alpamon"))
	_, _ = fmt.Fprintln(ios.ErrOut)
	for _, installCmd := range guide.InstallCommands {
		printGuideCommand(ios, ios.ErrOut, installCmd)
	}
	_, _ = fmt.Fprintln(ios.ErrOut)

	_, _ = fmt.Fprintln(ios.ErrOut, ios.Bold("Step 2 — Register"))
	_, _ = fmt.Fprintln(ios.ErrOut)
	printGuideCommand(ios, ios.ErrOut, guide.RegisterCommand)
	_, _ = fmt.Fprintln(ios.ErrOut)

	printGuideVerifyFooter(ios, 3)
}

func displayAnsibleGuideFromJSON(ios *utils.IOStreams, guide server.AnsibleGuideJsonResponse) {
	printGuideHeader(ios, guide.PlatformLabel, guide.ServerName, guide.AlpaconURL)

	_, _ = fmt.Fprintln(ios.ErrOut, ios.Bold("Step 1 — Install Ansible collection"))
	_, _ = fmt.Fprintln(ios.ErrOut)
	printGuideCommand(ios, ios.ErrOut, guide.CollectionInstall)
	_, _ = fmt.Fprintln(ios.ErrOut)

	_, _ = fmt.Fprintln(ios.ErrOut, ios.Bold("Step 2 — Configure inventory"))
	_, _ = fmt.Fprintln(ios.ErrOut)
	_, _ = fmt.Fprintln(ios.ErrOut, "Save as inventory.ini:")
	_, _ = fmt.Fprintln(ios.ErrOut)
	printGuideCommand(ios, ios.ErrOut, guide.InventorySnippet)
	_, _ = fmt.Fprintln(ios.ErrOut)

	_, _ = fmt.Fprintln(ios.ErrOut, ios.Bold("Step 3 — Register"))
	_, _ = fmt.Fprintln(ios.ErrOut)
	_, _ = fmt.Fprintln(ios.ErrOut, ios.Bold("Option A — Bundled playbook (recommended):"))
	_, _ = fmt.Fprintln(ios.ErrOut)
	printGuideCommand(ios, ios.ErrOut, guide.RunCommandQuick)
	_, _ = fmt.Fprintln(ios.ErrOut)
	_, _ = fmt.Fprintln(ios.ErrOut, ios.Bold("Option B — Custom playbook:"))
	_, _ = fmt.Fprintln(ios.ErrOut)
	_, _ = fmt.Fprintln(ios.ErrOut, "Save as playbook.yml:")
	_, _ = fmt.Fprintln(ios.ErrOut)
	printGuideCommand(ios, ios.ErrOut, guide.PlaybookSnippet)
	_, _ = fmt.Fprintln(ios.ErrOut)
	_, _ = fmt.Fprintln(ios.ErrOut, "Then run:")
	_, _ = fmt.Fprintln(ios.ErrOut)
	printGuideCommand(ios, ios.ErrOut, guide.RunCommandCustom)
	_, _ = fmt.Fprintln(ios.ErrOut)

	printGuideVerifyFooter(ios, 4)
}
