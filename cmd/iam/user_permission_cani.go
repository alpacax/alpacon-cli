package iam

import (
	"fmt"
	"os"
	"strings"

	"github.com/alpacax/alpacon-cli/api/rbac"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var userPermissionCanICmd = &cobra.Command{
	Use:   "can-i [USER] PERMISSION",
	Short: "Check whether a permission is allowed",
	Long: `Check whether a user holds a permission, and print yes or no.

With one argument the subject is you; with two, the user comes first. A permission
is written resource:action, and may use a wildcard—'server:*' asks whether the user
holds any server permission at all.

The question asked is the workspace-wide one. A permission held only through an
object-scoped binding answers no here, because the command names no object; use
'alpacon user permission ls --patterns' to see those.

Without -q the exit code is 0 for both answers, so a denial is never confused with a
failed request. With -q the answer is never printed, --output json included, and a
denial and a failed check share exit 1—only the failure writes a line to stderr. To
tell them apart in a script, drop -q and read the allowed field of --output json.`,
	Example: `  alpacon user permission can-i server:update
  alpacon user permission can-i john server:update
  alpacon user permission can-i john rbac:manage --explain
  alpacon user permission can-i john server:update -q && echo allowed`,
	Args: cobra.RangeArgs(1, 2),
	Run: func(cmd *cobra.Command, args []string) {
		quiet, _ := cmd.Flags().GetBool("quiet")
		explain, _ := cmd.Flags().GetBool("explain")

		subjectArgs, permission := splitCanIArgs(args)

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		subj := resolveSubject(alpaconClient, subjectArgs)

		if explain {
			explanation, err := rbac.ExplainPermission(alpaconClient, subj.ID, permission)
			if err != nil {
				utils.CliErrorWithExit("Failed to explain the decision: %s.", describeRBACError(alpaconClient, gateRoleRead, err))
			}

			utils.PrintJson(explanation)
			return
		}

		allowed, err := rbac.CheckPermission(alpaconClient, subj.PK, permission)
		if err != nil {
			utils.CliErrorWithExit("Failed to check the permission: %s.", describeRBACError(alpaconClient, gatePermissionIntrospect, err))
		}

		if quiet {
			if !allowed {
				os.Exit(utils.ExitCodeGeneralError)
			}
			return
		}

		if utils.OutputFormat == utils.OutputFormatJSON {
			result := map[string]any{"user": subj.Label, "permission": permission, "allowed": allowed}
			if err = utils.PrintJSONValue(os.Stdout, result); err != nil {
				utils.CliErrorWithExit("Failed to render the answer: %s.", err)
			}
			return
		}

		if allowed {
			fmt.Println("yes")
			return
		}
		fmt.Println("no")
	},
}

func init() {
	userPermissionCanICmd.Flags().BoolP("quiet", "q", false, "Print nothing; the exit code is the answer (0 allowed, 1 denied or the check failed)")
	userPermissionCanICmd.Flags().Bool("explain", false, "Print the server's account of how the decision was reached")

	// --explain answers with an account rather than a boolean, so there is nothing for
	// --quiet to turn into an exit code.
	userPermissionCanICmd.MarkFlagsMutuallyExclusive("quiet", "explain")
}

func splitCanIArgs(args []string) ([]string, string) {
	if len(args) == 1 {
		requirePermission(args[0])
		return nil, args[0]
	}

	// Other CLIs put the subject last or not at all, so a transposed command line is worth
	// naming rather than sending as a permission nobody holds.
	if looksLikePermission(args[0]) && !looksLikePermission(args[1]) {
		utils.CliErrorWithExit("The user comes first: 'alpacon user permission can-i %s %s'.", args[1], args[0])
	}

	requirePermission(args[1])
	return args[:1], args[1]
}

func requirePermission(arg string) {
	if !looksLikePermission(arg) {
		utils.CliErrorWithExit("%q is not a permission. Write it as resource:action, for example server:update, or use a wildcard such as server:* or '*'.", arg)
	}
}

func looksLikePermission(arg string) bool {
	return strings.ContainsAny(arg, ":*")
}
