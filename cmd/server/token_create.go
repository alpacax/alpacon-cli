package server

import (
	"encoding/json"
	"fmt"
	"strings"

	iamAPI "github.com/alpacax/alpacon-cli/api/iam"
	serverAPI "github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var tokenCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new server registration token",
	Long: `Generates a new server registration token.
The token key is displayed once at creation time and cannot be retrieved again.`,
	Example: `
    alpacon server token create --name k8s-nodes --groups infra --expires-in-days 365
    alpacon server token create  # interactive mode`,
	Run: func(cmd *cobra.Command, args []string) {
		name, _ := cmd.Flags().GetString("name")
		groups, _ := cmd.Flags().GetStringSlice("groups")
		expiresInDays, _ := cmd.Flags().GetInt("expires-in-days")

		if expiresInDays < 0 {
			utils.CliErrorWithExit("--expires-in-days must be 0 (no expiry) or a positive number, got %d.", expiresInDays)
		}

		interactive := name == ""

		if name == "" {
			name = utils.PromptForRequiredInput("Token name: ")
		}
		if interactive && len(groups) == 0 {
			groups = utils.PromptForListInput("Allowed group names or UUIDs (comma-separated, optional): ")
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		groupIDs, err := resolveGroupIDs(alpaconClient, groups)
		if err != nil {
			utils.CliErrorWithExit("Failed to resolve groups: %s.", err)
		}

		req := serverAPI.RegistrationTokenRequest{
			Name:          name,
			AllowedGroups: groupIDs,
		}
		if expiresInDays > 0 {
			req.ExpiresAt = utils.TimeFormat(expiresInDays)
		}

		resp, err := serverAPI.CreateRegistrationToken(alpaconClient, req)
		if err != nil {
			utils.CliErrorWithExit("Failed to create registration token: %s.", err)
		}

		if utils.OutputFormat == utils.OutputFormatJSON {
			data, err := json.Marshal(resp)
			if err != nil {
				utils.CliErrorWithExit("Failed to marshal response: %s.", err)
			}
			utils.PrintJson(data)
			return
		}

		utils.CliSuccess("Registration token created: %s", resp.Name)
		utils.CliWarning("Save this key now—it will not be shown again: %s", utils.Green(resp.Key))
		if resp.ExpiresAt != nil {
			utils.CliInfo("Token expires at: %s", *resp.ExpiresAt)
		}
	},
}

func init() {
	tokenCreateCmd.Flags().StringP("name", "n", "", "A name to identify the token.")
	tokenCreateCmd.Flags().StringSliceP("groups", "g", []string{}, "Group names or UUIDs to assign on registration (comma-separated).")
	tokenCreateCmd.Flags().Int("expires-in-days", 0, "Number of days until the token expires (0 = no expiry).")
}

// resolveGroupIDs converts a list of group names or UUIDs to UUIDs.
// Entries that are already UUIDs pass through unchanged.
// Entries that look like names are resolved via the IAM API.
func resolveGroupIDs(ac *client.AlpaconClient, entries []string) ([]string, error) {
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if utils.IsUUID(entry) {
			ids = append(ids, entry)
		} else {
			id, err := iamAPI.GetGroupIDByName(ac, entry)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve group %q: %w", entry, err)
			}
			ids = append(ids, id)
		}
	}
	return ids, nil
}
