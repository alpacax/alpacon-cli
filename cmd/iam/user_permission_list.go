package iam

import (
	"os"

	"github.com/alpacax/alpacon-cli/api/rbac"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var userPermissionListCmd = &cobra.Command{
	Use:     "ls [USER]",
	Aliases: []string{"list"},
	Short:   "List a user's effective permissions",
	Long: `Show what a user's roles let them do, and which role confers each.

With no USER the subject is you.

By default this reports provenance: the roles in effect, whether each is bound
directly or inherited from a group, and the capabilities they add up to. Roles
scoped to a single object are left out—their number grows with the number of
objects, so listing them would be unbounded. They are visible in
'alpacon user role ls'.

--patterns switches to the raw permission patterns instead, split into the ones
granted workspace-wide and the ones reachable only through a narrower binding. The
second group says the user holds the permission somewhere without saying on what.`,
	Example: `  alpacon user permission ls
  alpacon user permission ls john
  alpacon user permission ls john --patterns --output json`,
	Args: cobra.MaximumNArgs(1),
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		patternsOnly, _ := cmd.Flags().GetBool("patterns")

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			return utils.NewExitError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		subj, err := resolveSubject(alpaconClient, args)
		if err != nil {
			return err
		}

		if patternsOnly {
			patterns, err := rbac.GetPermissionPatterns(alpaconClient, subj.PK)
			if err != nil {
				return utils.NewExitError("Failed to retrieve %s's permissions: %s.", subj.Label, describeRBACError(alpaconClient, gatePermissionIntrospect, err))
			}

			return utils.PrintTable(rbac.PermissionPatternAttributesFrom(patterns))
		}

		effective, err := rbac.GetEffectivePermissions(alpaconClient, subj.PK)
		if err != nil {
			return utils.NewExitError("Failed to retrieve %s's permissions: %s.", subj.Label, describeRBACError(alpaconClient, gateUserRead, err))
		}

		roleRows := rbac.EffectiveRoleAttributesFrom(effective.Roles)
		capabilityRows := rbac.ScopeAttributesFrom(&effective.Permissions)

		if utils.OutputFormat == utils.OutputFormatJSON {
			// The same projections the table prints, so both output modes answer the same question.
			combined := map[string]any{
				"user":        effective.User,
				"summary":     effective.Summary,
				"roles":       jsonSlice(roleRows),
				"permissions": jsonSlice(capabilityRows),
			}
			if err = utils.PrintJSONValue(os.Stdout, combined); err != nil {
				return utils.NewExitError("Failed to render the permissions: %s.", err)
			}
			return nil
		}

		utils.PrintHeader("Roles in effect")
		if err := utils.PrintTable(roleRows); err != nil {
			return err
		}
		utils.PrintHeader("Permissions")
		return utils.PrintTable(capabilityRows)
	}),
}

func init() {
	userPermissionListCmd.Flags().Bool("patterns", false, "List the raw permission patterns instead of the roles conferring them")
}
