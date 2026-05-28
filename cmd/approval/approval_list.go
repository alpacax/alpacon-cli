package approval

import (
	"fmt"
	"slices"
	"strings"

	approvalapi "github.com/alpacax/alpacon-cli/api/approval"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var approvalListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List approval requests",
	Long: `List approval requests. Defaults to pending status.

Superusers see all workspace requests. Non-superusers are
restricted to their own requests (equivalent to --my).`,
	Example: `  alpacon approval ls
  alpacon approval ls --status approved
  alpacon approval ls --type sudo
  alpacon approval ls --type work_session --status pending
  alpacon approval ls --my
  alpacon approval ls --my --status approved`,
	Run: func(cmd *cobra.Command, args []string) {
		if err := validateStatusFilter(statusFilter); err != nil {
			utils.CliErrorWithExit("%s.", err)
		}
		if err := validateTypeFilter(typeFilter); err != nil {
			utils.CliErrorWithExit("%s.", err)
		}

		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		var requests []approvalapi.ApprovalRequestAttributes
		if myRequests {
			requests, err = approvalapi.ListMyApprovalRequests(ac, statusFilter, typeFilter)
		} else {
			requests, err = approvalapi.ListApprovalRequests(ac, statusFilter, typeFilter)
		}
		if err != nil {
			utils.CliErrorWithExit("Failed to list approval requests: %s.", err)
		}

		utils.PrintTable(requests)
	},
}

func init() {
	approvalListCmd.Flags().StringVar(&statusFilter, "status", "pending", "Filter by status: pending|approved|rejected|cancelled|expired")
	approvalListCmd.Flags().StringVar(&typeFilter, "type", "", "Filter by request type: sudo|work_session|username|groupname|service_token|svc_token_mod|app_username|work_session_mod|sudo_policy")
	approvalListCmd.Flags().BoolVar(&myRequests, "my", false, "Show only requests you submitted")
}

var validStatuses = []string{"pending", "approved", "rejected", "cancelled", "expired"}

var validTypes = []string{
	"sudo", "work_session", "username", "groupname", "service_token",
	"svc_token_mod", "app_username", "work_session_mod", "sudo_policy",
}

func validateStatusFilter(s string) error {
	if s == "" || slices.Contains(validStatuses, s) {
		return nil
	}
	return fmt.Errorf("invalid --status %q: must be one of %s", s, strings.Join(validStatuses, ", "))
}

func validateTypeFilter(s string) error {
	if s == "" || slices.Contains(validTypes, s) {
		return nil
	}
	return fmt.Errorf("invalid --type %q: must be one of %s", s, strings.Join(validTypes, ", "))
}
