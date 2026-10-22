package acl

import (
	"github.com/alpacax/alpacon-cli/api/acl"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var entryCmd = &cobra.Command{
	Use:   "entry",
	Short: "Manage pre-approved command entries",
	Long: `Manage the entries of the command pre-approval ledger. Each entry pre-approves
one command pattern for one service token, run as one user, on a set of servers
or on every server in the workspace.

An entry is issued or revoked, never edited: every field says what it
authorizes, so a change is a new entry. Disabling one stops it matching while
keeping it in place.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return utils.RequireSubcommand(cmd)
	},
}

func init() {
	entryCmd.AddCommand(entryListCmd)
	entryCmd.AddCommand(entryAddCmd)
	entryCmd.AddCommand(entryRemoveCmd)
	entryCmd.AddCommand(entryDisableCmd)
	entryCmd.AddCommand(entryEnableCmd)
}

var entryListCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List pre-approved commands",
	Long: `List the workspace's active pre-approved commands, or one service token's
with --token. Revoked entries are not listed.`,
	Example: `  alpacon acl entry ls
  alpacon acl entry ls --token deploy-bot
  alpacon acl entry ls --output json`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		tokenRef, _ := cmd.Flags().GetString("token")

		principalID := ""
		if tokenRef != "" {
			var err error
			if principalID, err = resolveServiceToken(tokenRef); err != nil {
				utils.CliErrorWithExit("Failed to find service token %q: %s.", tokenRef, err)
			}
		}

		session := openLedger()
		entries, err := acl.ListEntries(session.signer, session.workspaceID, principalID)
		if err != nil {
			session.fail("list pre-approved commands", err)
		}

		tokenNames, serverNames := workspaceNames()
		utils.PrintTable(acl.ToEntryAttributes(entries, tokenNames, serverNames))
	},
}

func init() {
	entryListCmd.Flags().String("token", "", "List only this service token's entries (name or ID)")
}
