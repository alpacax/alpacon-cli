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
with --token. Revoked entries are not listed. APPLIES is yes when the entry
applies, no when it does not, and blank when the application account cannot be
resolved.`,
	Example: `  alpacon acl entry ls
  alpacon acl entry ls --token deploy-bot
  alpacon acl entry ls --output json`,
	Args: cobra.NoArgs,
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		tokenRef, _ := cmd.Flags().GetString("token")

		session, err := openLedger()
		if err != nil {
			return err
		}
		principalID := ""
		if tokenRef != "" {
			if principalID, err = session.listFilterID(tokenRef); err != nil {
				return utils.NewExitError("Failed to find service token %q: %s.", tokenRef, err)
			}
		}

		entries, err := acl.ListEntries(session.signer, session.workspaceID, principalID)
		if err != nil {
			return session.fail("list pre-approved commands", err)
		}

		tokenNames, serverNames, tokenAccounts := session.workspaceNames(entries)
		return utils.PrintTable(ledgerEntryAttributes(entries, tokenNames, serverNames, tokenAccounts))
	}),
}

func init() {
	entryListCmd.Flags().String("token", "", "List only this service token's entries (name or ID)")
}

func ledgerEntryAttributes(entries []acl.EntryResponse, tokenNames, serverNames, tokenAccounts map[string]string) []acl.EntryAttributes {
	rows := acl.ToEntryAttributes(entries, tokenNames, serverNames)
	for i, entry := range entries {
		if username := tokenAccounts[entry.PrincipalID]; entry.PrincipalType == acl.PrincipalTypeServiceToken && username != "" {
			applies := accountMatches(entry.Username, username) && accountMatches(entry.Groupname, username)
			rows[i].AppliesToToken = &applies
			if applies {
				rows[i].Applies = "yes"
			} else {
				rows[i].Applies = "no"
			}
		}
	}
	return rows
}

func accountMatches(value, own string) bool {
	return value == "" || value == "*" || value == own
}
