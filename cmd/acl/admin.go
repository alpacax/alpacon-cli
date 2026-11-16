package acl

import (
	"github.com/alpacax/alpacon-cli/api/acl"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var adminCmd = &cobra.Command{
	Use:   "admin",
	Short: "Manage Command ACL admins",
	Long: `Manage who holds the Command ACL admin role in this workspace: the people
who may read and change its pre-approved commands.

The first Command ACL admin is assigned when the workspace is created; every
one after that is granted by an existing admin. The command signer refuses to
revoke the last one, so grant another admin first.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return utils.RequireSubcommand(cmd)
	},
}

func init() {
	adminCmd.AddCommand(adminListCmd)
	adminCmd.AddCommand(adminGrantCmd)
	adminCmd.AddCommand(adminRevokeCmd)
}

var adminListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List Command ACL admins",
	Example: `  alpacon acl admin ls`,
	Args:    cobra.NoArgs,
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		session, err := openLedger()
		if err != nil {
			return err
		}
		admins, err := acl.ListAdmins(session.signer, session.workspaceID)
		if err != nil {
			return session.fail("list Command ACL admins", err)
		}
		return utils.PrintTable(acl.ToAdminAttributes(admins))
	}),
}

var adminGrantCmd = &cobra.Command{
	Use:   "grant SUBJECT",
	Short: "Grant the Command ACL admin role",
	Long: `Grant a person the Command ACL admin role in this workspace.

SUBJECT is the person's sign-in identifier (such as auth0|65f0c1d2e3b4a5c6d7e8f901),
the value 'alpacon acl admin ls' prints for the admins already granted. The
email is recorded for people to read; the subject is what grants the role. A
machine identity cannot hold it.`,
	Example: `  alpacon acl admin grant 'auth0|65f0c1d2e3b4a5c6d7e8f901' --email alice@example.com`,
	Args:    cobra.ExactArgs(1),
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		email, _ := cmd.Flags().GetString("email")

		session, err := openLedger()
		if err != nil {
			return err
		}
		admin, err := acl.GrantAdmin(session.signer, session.workspaceID, args[0], email)
		if err != nil {
			return session.fail("grant the Command ACL admin role", err)
		}
		utils.CliSuccess("Granted the Command ACL admin role to %s (%s)", admin.Subject, admin.ID)
		return nil
	}),
}

var adminRevokeCmd = &cobra.Command{
	Use:   "revoke ADMIN",
	Short: "Revoke the Command ACL admin role",
	Long: `Revoke a person's Command ACL admin role in this workspace. ADMIN is their
subject, their email, or the grant ID 'alpacon acl admin ls' prints.

The command signer refuses to revoke the last Command ACL admin, which would
leave nobody able to manage the workspace's pre-approved commands.`,
	Example: `  alpacon acl admin revoke alice@example.com
  alpacon acl admin revoke 'auth0|65f0c1d2e3b4a5c6d7e8f901' -y`,
	Args: cobra.ExactArgs(1),
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		yes, _ := cmd.Flags().GetBool("yes")

		session, err := openLedger()
		if err != nil {
			return err
		}
		admins, err := acl.ListAdmins(session.signer, session.workspaceID)
		if err != nil {
			return session.fail("list Command ACL admins", err)
		}
		admin, err := acl.FindAdmin(admins, args[0])
		if err != nil {
			return utils.NewExitError("%s.", upperFirst(err.Error()))
		}

		if !yes {
			if proceed, err := utils.Confirm("Revoke the Command ACL admin role from %s?", adminLabel(admin)); err != nil || !proceed {
				return err
			}
		}
		if err = acl.RevokeAdmin(session.signer, admin.ID); err != nil {
			return session.fail("revoke the Command ACL admin role", err)
		}
		utils.CliSuccess("Revoked the Command ACL admin role from %s", adminLabel(admin))
		return nil
	}),
}

func init() {
	adminGrantCmd.Flags().String("email", "", "Email recorded with the grant")
	adminRevokeCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
}

func adminLabel(admin acl.AdminResponse) string {
	if admin.Email != "" {
		return admin.Email + " (" + admin.Subject + ")"
	}
	return admin.Subject
}
