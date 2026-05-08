package worksession

import (
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var (
	extendExpiresIn string
	extendExpiresAt string
)

var workSessionExtendCmd = &cobra.Command{
	Use:   "extend SESSION_ID",
	Short: "Extend the expiry of an approved or active work session",
	Args:  cobra.ExactArgs(1),
	Example: `  alpacon work-session extend ses-abc123 --expires-in 2h
  alpacon work-session extend ses-abc123 --expires-at 2026-05-09T10:00:00Z`,
	Run: func(cmd *cobra.Command, args []string) {
		expiresAtVal, err := parseExpiryFlag(extendExpiresIn, extendExpiresAt)
		if err != nil {
			utils.CliErrorWithExit("Invalid expiry: %s.", err)
		}

		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		req := wsapi.WorkSessionExtendRequest{ExpiresAt: expiresAtVal}
		if err := wsapi.ExtendWorkSession(ac, args[0], req); err != nil {
			utils.CliErrorWithExit("Failed to extend work session: %s.", err)
		}

		utils.CliSuccess("Work session %s extended to %s.", args[0], expiresAtVal)
	},
}

func init() {
	workSessionExtendCmd.Flags().StringVar(&extendExpiresIn, "expires-in", "", "Additional duration (e.g. 2h)")
	workSessionExtendCmd.Flags().StringVar(&extendExpiresAt, "expires-at", "", "New absolute expiry time (RFC3339)")
	WorkSessionCmd.AddCommand(workSessionExtendCmd)
}
