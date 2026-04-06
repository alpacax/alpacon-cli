package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

type whoamiOutput struct {
	Username      string                `json:"username,omitempty"`
	Email         string                `json:"email,omitempty"`
	Phone         string                `json:"phone,omitempty"`
	WorkspaceName string                `json:"workspace_name"`
	WorkspaceURL  string                `json:"workspace_url"`
	AuthMethod    string                `json:"auth_method"`
	ExpiresAt     string                `json:"expires_at,omitempty"`
	UID           int                   `json:"uid,omitempty"`
	Shell         string                `json:"shell,omitempty"`
	HomeDirectory string                `json:"home_directory,omitempty"`
	Role          string                `json:"role,omitempty"`
	Groups        []iam.GroupMembership `json:"groups,omitempty"`
}

var whoamiCmd = &cobra.Command{
	Use:   "whoami [flags]",
	Short: "Display current authenticated identity",
	Long: `Display the current authenticated identity, workspace, system user info,
and permissions. Useful for verifying context before running infrastructure
commands, especially for AI agents and operators managing multiple workspaces.`,
	Example: `  alpacon whoami
  alpacon whoami --json`,
	Run: func(cmd *cobra.Command, args []string) {
		jsonFlag, _ := cmd.Flags().GetBool("json")

		cfg, err := config.LoadConfig()
		if err != nil {
			utils.CliErrorWithExit("Not logged in. Run 'alpacon login' to authenticate.")
		}

		output := whoamiOutput{
			WorkspaceName: cfg.WorkspaceName,
			WorkspaceURL:  cfg.WorkspaceURL,
			AuthMethod:    getAuthMethod(cfg),
			ExpiresAt:     getExpiresAt(cfg),
		}

		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliWarning("Could not create authenticated API client: %s", err)
			utils.CliWarning("Showing local config only. Server fields are unavailable.")
			warnIfExpiringSoon(cfg)
			printWhoami(output, jsonFlag)
			return
		}

		user, err := iam.GetCurrentUser(ac)
		if err != nil {
			utils.CliWarning("Could not fetch user info: %s", err)
			warnIfExpiringSoon(cfg)
			printWhoami(output, jsonFlag)
			return
		}

		output.Username = user.Username
		output.Email = user.Email
		output.Phone = user.Phone
		output.UID = user.UID
		output.Shell = user.Shell
		output.HomeDirectory = user.HomeDirectory
		output.Role = getRole(user.IsStaff, user.IsSuperuser)

		groups, err := iam.GetUserMemberships(ac, user.ID)
		if err != nil {
			utils.CliWarning("Could not fetch group memberships: %s", err)
		} else {
			output.Groups = groups
		}

		warnIfExpiringSoon(cfg)
		printWhoami(output, jsonFlag)
	},
}

func init() {
	whoamiCmd.Flags().Bool("json", false, "Output in JSON format")
}

func getAuthMethod(cfg config.Config) string {
	if cfg.AccessToken != "" {
		return "Browser login"
	}
	if cfg.Token != "" {
		return "API token"
	}
	return "unknown"
}

func getExpiresAt(cfg config.Config) string {
	if cfg.Token != "" && cfg.ExpiresAt != "" {
		return cfg.ExpiresAt
	}
	return ""
}

func getRole(isStaff, isSuperuser bool) string {
	if isSuperuser {
		return "superuser"
	}
	if isStaff {
		return "staff"
	}
	return "user"
}

func isAPITokenAuth(cfg config.Config) bool {
	return cfg.Token != "" && cfg.AccessToken == ""
}

func warnIfExpiringSoon(cfg config.Config) {
	if !isAPITokenAuth(cfg) {
		return
	}

	expiresAt := getExpiresAt(cfg)
	if expiresAt == "" {
		return
	}

	expireTime, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return
	}

	remaining := time.Until(expireTime)
	if remaining < 0 {
		utils.CliWarning("Token has expired. Run 'alpacon login' to re-authenticate.")
	} else if remaining < time.Hour {
		utils.CliWarning("Token expires in %s. Consider re-authenticating soon.", formatDuration(remaining))
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

func formatExpiresHuman(expiresAt string) string {
	if expiresAt == "" {
		return ""
	}

	expireTime, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return expiresAt
	}

	remaining := time.Until(expireTime)
	local := expireTime.Local().Format("2006-01-02 15:04 MST")

	if remaining < 0 {
		return fmt.Sprintf("%s (expired)", local)
	}

	days := int(remaining.Hours() / 24)
	hours := int(remaining.Hours()) % 24

	if days > 0 {
		return fmt.Sprintf("%s (%dd %dh remaining)", local, days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%s (%dh %dm remaining)", local, hours, int(remaining.Minutes())%60)
	}
	return fmt.Sprintf("%s (%dm remaining)", local, int(remaining.Minutes()))
}

func formatGroups(groups []iam.GroupMembership) string {
	if len(groups) == 0 {
		return ""
	}

	parts := make([]string, len(groups))
	for i, g := range groups {
		parts[i] = fmt.Sprintf("%s (%s)", g.Name, g.Role)
	}
	return strings.Join(parts, ", ")
}

func printWhoami(output whoamiOutput, jsonOutput bool) {
	if jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(output)
		return
	}

	lines := []struct {
		label string
		value string
	}{
		{"User", output.Username},
		{"Email", output.Email},
		{"Phone", output.Phone},
		{"Workspace", fmt.Sprintf("%s (%s)", output.WorkspaceName, output.WorkspaceURL)},
		{"Auth", output.AuthMethod},
		{"Expires", formatExpiresHuman(output.ExpiresAt)},
		{"UID", fmt.Sprintf("%d", output.UID)},
		{"Shell", output.Shell},
		{"Home", output.HomeDirectory},
		{"Role", output.Role},
		{"Groups", formatGroups(output.Groups)},
	}

	for _, l := range lines {
		if l.value == "" || l.value == "0" {
			continue
		}
		fmt.Fprintf(os.Stdout, "%-13s%s\n", l.label+":", l.value)
	}
}
