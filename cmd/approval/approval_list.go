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

var (
	statusFilter string
	typeFilter   string
	myRequests   bool
)

var validStatuses = []string{"pending", "approved", "rejected", "cancelled", "expired"}

var validTypes = []string{
	"sudo", "work_session", "username", "groupname", "service_token",
	"svc_token_mod", "app_username", "work_session_mod", "sudo_policy",
}

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

		// Server enforces /api/approvals/approvals/ as superuser-only. Auto-fall back to
		// the my-requests endpoint for non-superusers so the default `approval ls` does not
		// return a confusing 403.
		useMyEndpoint := myRequests
		if !useMyEndpoint {
			if err := ac.LoadCurrentUser(); err != nil {
				utils.CliErrorWithExit("Failed to load current user: %s.", err)
			}
			if ac.Privileges != "superuser" {
				useMyEndpoint = true
			}
		}

		var requests []approvalapi.ApprovalRequestAttributes
		if useMyEndpoint {
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

func validateEnumFlag(flag, value string, valid []string) error {
	if value == "" || slices.Contains(valid, value) {
		return nil
	}
	return fmt.Errorf("invalid --%s %q: must be one of %s", flag, value, strings.Join(valid, ", "))
}

func validateStatusFilter(s string) error { return validateEnumFlag("status", s, validStatuses) }
func validateTypeFilter(s string) error    { return validateEnumFlag("type", s, validTypes) }
