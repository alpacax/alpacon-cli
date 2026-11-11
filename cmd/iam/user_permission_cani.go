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

var userPermissionCanICmd = newUserPermissionCanICmd(client.NewAlpaconAPIClient)

func newUserPermissionCanICmd(newClient func() (*client.AlpaconClient, error)) *cobra.Command {
	cmd := &cobra.Command{
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
		RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
			quiet, _ := cmd.Flags().GetBool("quiet")
			explain, _ := cmd.Flags().GetBool("explain")

			subjectArgs, permission, err := splitCanIArgs(args)
			if err != nil {
				return err
			}

			alpaconClient, err := newClient()
			if err != nil {
				return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			}

			subj, err := resolveSubject(alpaconClient, subjectArgs)
			if err != nil {
				return err
			}

			if explain {
				explanation, err := rbac.ExplainPermission(alpaconClient, subj.ID, permission)
				if err != nil {
					return utils.NewExitError("Failed to explain the decision: %s.", describeRBACError(alpaconClient, gateRoleRead, err))
				}

				utils.PrintJson(explanation)
				return nil
			}

			allowed, err := rbac.CheckPermission(alpaconClient, subj.PK, permission)
			if err != nil {
				return utils.NewExitError("Failed to check the permission: %s.", describeRBACError(alpaconClient, gatePermissionIntrospect, err))
			}

			if quiet {
				if !allowed {
					return utils.NewSilentExitError(utils.ExitCodeGeneralError)
				}
				return nil
			}

			if utils.OutputFormat == utils.OutputFormatJSON {
				result := map[string]any{"user": subj.Label, "permission": permission, "allowed": allowed}
				if err = utils.PrintJSONValue(os.Stdout, result); err != nil {
					return utils.NewExitError("Failed to render the answer: %s.", err)
				}
				return nil
			}

			if allowed {
				fmt.Println("yes")
				return nil
			}
			fmt.Println("no")
			return nil
		}),
	}
	cmd.Flags().BoolP("quiet", "q", false, "Print nothing; the exit code is the answer (0 allowed, 1 denied or the check failed, 2 a rejected argument)")
	cmd.Flags().Bool("explain", false, "Print the server's account of how the decision was reached")

	// --explain answers with an account rather than a boolean, so there is nothing for
	// --quiet to turn into an exit code.
	cmd.MarkFlagsMutuallyExclusive("quiet", "explain")
	return cmd
}

func splitCanIArgs(args []string) ([]string, string, error) {
	if len(args) == 1 {
		if err := checkPermission(args[0]); err != nil {
			return nil, "", err
		}
		return nil, args[0], nil
	}

	// Other CLIs put the subject last or not at all, so a transposed command line is worth
	// naming rather than sending as a permission nobody holds.
	if looksLikePermission(args[0]) && !looksLikePermission(args[1]) {
		return nil, "", utils.NewUsageExitError("The user comes first: 'alpacon user permission can-i %s %s'.", args[1], args[0])
	}

	if err := checkPermission(args[1]); err != nil {
		return nil, "", err
	}
	return args[:1], args[1], nil
}

func checkPermission(arg string) error {
	if !looksLikePermission(arg) {
		return utils.NewUsageExitError("%q is not a permission. Write it as resource:action, for example server:update, or use a wildcard such as server:* or '*'.", arg)
	}
	return nil
}

func looksLikePermission(arg string) bool {
	return strings.ContainsAny(arg, ":*")
}
