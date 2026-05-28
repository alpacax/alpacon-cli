package approval

import (
	approvalapi "github.com/alpacax/alpacon-cli/api/approval"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

type describeRow struct {
	Field string `table:"Field"`
	Value string `table:"Value"`
}

var approvalDescribeCmd = &cobra.Command{
	Use:     "describe REQUEST_ID",
	Aliases: []string{"desc"},
	Short:   "Show details of an approval request",
	Args:    cobra.ExactArgs(1),
	Example: `  alpacon approval describe apr-abc123
  alpacon approval desc apr-abc123`,
	Run: func(cmd *cobra.Command, args []string) {
		ac, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		if utils.OutputFormat == utils.OutputFormatJSON {
			body, err := approvalapi.GetApprovalRequestRaw(ac, args[0])
			if err != nil {
				utils.CliErrorWithExit("Failed to retrieve approval request: %s.", err)
			}
			utils.PrintJson(body)
			return
		}

		req, err := approvalapi.GetApprovalRequest(ac, args[0])
		if err != nil {
			utils.CliErrorWithExit("Failed to retrieve approval request: %s.", err)
		}

		requestedBy := ""
		if req.RequestedBy != nil {
			requestedBy = req.RequestedBy.Name
		}
		reviewedBy := ""
		if req.ReviewedBy != nil {
			reviewedBy = req.ReviewedBy.Name
		}
		reviewedAt := ""
		if req.ReviewedAt != nil {
			reviewedAt = req.ReviewedAt.Local().Format("2006-01-02 15:04")
		}

		rows := []describeRow{
			{"ID", req.ID},
			{"Type", req.RequestType},
			{"Status", req.Status},
			{"Request", req.RequestData},
			{"Description", req.Description},
			{"Requested by", requestedBy},
			{"Reviewed by", reviewedBy},
			{"Reviewed at", reviewedAt},
			{"Added at", req.AddedAt.Local().Format("2006-01-02 15:04")},
		}
		utils.PrintTable(rows)
	},
}
