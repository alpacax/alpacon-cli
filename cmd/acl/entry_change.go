package acl

import (
	"errors"
	"fmt"
	"strings"

	"github.com/alpacax/alpacon-cli/api/acl"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var entryRemoveCmd = &cobra.Command{
	Use:     "remove ENTRY_ID...",
	Aliases: []string{"rm"},
	Short:   "Revoke pre-approved commands",
	Long: `Revoke one or more pre-approved commands by entry ID, taken from
'alpacon acl entry ls'. Several IDs are revoked together or not at all.

A revoked entry stops matching at once, and the command it pre-approved is
judged on every run again. The ledger keeps the revoked entry for its audit
trail; to stop an entry for a while instead, disable it.`,
	Example: `  alpacon acl entry remove 550e8400-e29b-41d4-a716-446655440000
  alpacon acl entry rm 550e8400-e29b-41d4-a716-446655440000 3f1c9a2e-7b4d-4e8a-9c6f-0d2b5e8a1f47 -y`,
	Args: cobra.MinimumNArgs(1),
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		if err := requireEntryIDs(args); err != nil {
			return err
		}

		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			message := fmt.Sprintf("Revoke %d pre-approved command entries?", len(args))
			if len(args) == 1 {
				message = fmt.Sprintf("Revoke pre-approved command entry %s?", args[0])
			}
			if proceed, err := utils.Confirm("%s", message); err != nil || !proceed {
				return err
			}
		}

		session, err := openLedger()
		if err != nil {
			return err
		}
		if err := session.requireEntriesHere(args); err != nil {
			return err
		}
		if len(args) == 1 {
			if err := acl.RevokeEntry(session.signer, args[0]); err != nil {
				return session.fail("revoke the entry", err)
			}
			utils.CliSuccess("Revoked entry %s", args[0])
			return nil
		}

		revoked, err := acl.RevokeEntries(session.signer, args)
		if err != nil {
			return session.fail("revoke the entries", err)
		}
		utils.CliSuccess("Revoked %d entries", revoked)
		return nil
	}),
}

var entryDisableCmd = &cobra.Command{
	Use:   "disable ENTRY_ID",
	Short: "Stop a pre-approved command matching",
	Long: `Stop a pre-approved command matching without revoking it. The entry stays
in the ledger and 'alpacon acl entry enable' turns it back on.`,
	Example: `  alpacon acl entry disable 550e8400-e29b-41d4-a716-446655440000`,
	Args:    cobra.ExactArgs(1),
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		return setEntryEnabled(args[0], false)
	}),
}

var entryEnableCmd = &cobra.Command{
	Use:     "enable ENTRY_ID",
	Short:   "Let a disabled pre-approved command match again",
	Example: `  alpacon acl entry enable 550e8400-e29b-41d4-a716-446655440000`,
	Args:    cobra.ExactArgs(1),
	RunE: utils.WithExitError(func(cmd *cobra.Command, args []string) error {
		return setEntryEnabled(args[0], true)
	}),
}

func init() {
	entryRemoveCmd.Flags().BoolP("yes", "y", false, "Skip confirmation prompt")
}

func setEntryEnabled(entryID string, enabled bool) error {
	if err := requireEntryIDs([]string{entryID}); err != nil {
		return err
	}

	session, err := openLedger()
	if err != nil {
		return err
	}
	if err := session.requireEntriesHere([]string{entryID}); err != nil {
		return err
	}
	action, done := "disable the entry", "Disabled"
	if enabled {
		action, done = "enable the entry", "Enabled"
	}
	entry, err := acl.SetEntryEnabled(session.signer, entryID, enabled)
	if err != nil {
		return session.fail(action, err)
	}
	utils.CliSuccess("%s %q (%s)", done, entry.CommandPattern, entry.ID)
	return nil
}

// requireEntryIDs refuses anything but an entry ID before a request is made:
// entries carry no name, so a name here is a mistake rather than a lookup.
func requireEntryIDs(ids []string) error {
	for _, id := range ids {
		if !utils.IsUUID(id) {
			return utils.NewUsageExitError("%q is not an entry ID. Run 'alpacon acl entry ls' to find it.", id)
		}
	}
	return nil
}

// requireEntriesHere refuses an entry of another workspace: the signer finds an
// id in every workspace the caller administers, the session checked only this one.
func (s *ledgerSession) requireEntriesHere(ids []string) error {
	if err := s.checkEntriesHere(ids); err != nil {
		return utils.NewExitError("%s", sentence(upperFirst(err.Error())))
	}
	return nil
}

func (s *ledgerSession) checkEntriesHere(ids []string) error {
	for _, id := range ids {
		entry, err := acl.GetEntry(s.signer, id)
		if err != nil {
			s.dropRefusedToken(err)
			return errors.New(s.errorMessage("read entry "+id, err))
		}
		if !strings.EqualFold(entry.WorkspaceID, s.workspaceID) {
			return fmt.Errorf("entry %s belongs to another workspace. Switch to that workspace to change it", id)
		}
	}
	return nil
}
