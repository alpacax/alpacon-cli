package worksession

import (
	"errors"

	"github.com/spf13/cobra"
)

var (
	statusFilter    string
	requesterFilter string
)

var WorkSessionCmd = &cobra.Command{
	Use:     "work-session",
	Aliases: []string{"session"},
	Short:   "Create and manage work sessions",
	Long:    "Create, inspect, and manage work sessions—the approval-gated units that group Websh, WebFTP, and exec access on Alpacon.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := cmd.Help(); err != nil {
			return err
		}
		return errors.New("a subcommand is required. Use 'alpacon work-session ls', 'alpacon work-session create', 'alpacon work-session describe', 'alpacon work-session use', 'alpacon work-session activate', 'alpacon work-session complete', or 'alpacon work-session extend'. Run 'alpacon work-session --help' for more information")
	},
}

func init() {
	WorkSessionCmd.AddCommand(workSessionListCmd)
	WorkSessionCmd.AddCommand(workSessionCreateCmd)
	WorkSessionCmd.AddCommand(workSessionDescribeCmd)
	WorkSessionCmd.AddCommand(workSessionActivateCmd)
	WorkSessionCmd.AddCommand(workSessionCompleteCmd)
	WorkSessionCmd.AddCommand(workSessionExtendCmd)
	WorkSessionCmd.AddCommand(workSessionUseCmd)
}
