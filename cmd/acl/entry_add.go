package acl

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api/acl"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var entryAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Pre-approve a command for a service token",
	Long: `Pre-approve a command pattern for a service token.

Use * as a wildcard for arguments; a pattern without one pre-approves that exact
command line. --username is the account the command runs as, or "*" for any
account. Without --server the entry covers every server in the workspace, and
the command signer warns about it.

--file issues several entries at once from a JSON array whose items carry the
ledger's own fields: principal_id (a service token ID), command_pattern,
username, server_ids ([] covers every server), and optionally groupname,
expires_at and enabled. The batch is all or nothing: one
refused entry refuses the whole file. principal_type may be left out; it can
only be service_token.

Only a service token can be pre-approved commands: a personal API token has no
command list on the ledger. Existing entries for personal API tokens are still
listed and can be revoked.`,
	Example: `  alpacon acl entry add --token deploy-bot --command "systemctl restart nginx" \
    --username root --server web-01

  alpacon acl entry add --token deploy-bot --command "docker compose *" \
    --username deploy --server web-01,web-02 --expires-at 2027-01-15T10:00:00Z

  alpacon acl entry add --file entries.json`,
	Args: cobra.NoArgs,
	Run:  runEntryAdd,
}

func init() {
	entryAddCmd.Flags().String("token", "", "Service token the entry pre-approves the command for (name or ID)")
	entryAddCmd.Flags().StringP("command", "c", "", "Command pattern to pre-approve (supports * wildcard)")
	entryAddCmd.Flags().String("username", "", `Account the command runs as ("*" for any account)`)
	entryAddCmd.Flags().String("groupname", "", "Group the command runs as (default: no restriction)")
	entryAddCmd.Flags().StringSlice("server", nil, "Servers the entry covers, by name or ID (repeatable or comma-separated; default: every server)")
	entryAddCmd.Flags().String("expires-at", "", "Expiry time in RFC 3339 (default: no expiry)")
	entryAddCmd.Flags().Bool("disabled", false, "Issue the entry disabled")
	entryAddCmd.Flags().String("file", "", "Issue every entry in this JSON file")
}

func runEntryAdd(cmd *cobra.Command, args []string) {
	file, _ := cmd.Flags().GetString("file")

	var entries []acl.EntryRequest
	var flagged *flagEntry
	if file != "" {
		if flagged := entryFlagsSet(cmd); len(flagged) > 0 {
			utils.CliUsageErrorWithExit("--file cannot be combined with --%s.", flagged[0])
		}
		data, err := os.ReadFile(file)
		if err != nil {
			utils.CliErrorWithExit("Failed to read %s: %s.", file, err)
		}
		if entries, err = parseEntryFile(data); err != nil {
			utils.CliErrorWithExit("Failed to read %s: %s.", file, err)
		}
	} else {
		flagged = entryFromFlags(cmd)
	}

	session := openLedger()
	if flagged != nil {
		entries = []acl.EntryRequest{flagged.resolve(session)}
	} else {
		session.confirmServiceTokens(entries)
	}
	issued, err := acl.IssueEntries(session.signer, session.workspaceID, entries)
	if err != nil {
		session.fail("pre-approve the command", err)
	}

	printWarnings(issued)
	for _, entry := range issued {
		utils.CliSuccess("Pre-approved %q (%s)", entry.CommandPattern, entry.ID)
	}
}

// entryFlagsSet lists the single-entry flags that were given.
func entryFlagsSet(cmd *cobra.Command) []string {
	var set []string
	for _, name := range []string{"token", "command", "username", "groupname", "server", "expires-at", "disabled"} {
		if cmd.Flags().Changed(name) {
			set = append(set, name)
		}
	}
	return set
}

// flagEntry is the one entry the flags describe, before its service token and
// server names are resolved in the session's workspace.
type flagEntry struct {
	tokenRef   string
	serverRefs []string
	request    acl.EntryRequest
}

// entryFromFlags reads the one entry the flags describe, exiting on a missing
// or malformed value before anything reaches the signer.
func entryFromFlags(cmd *cobra.Command) *flagEntry {
	tokenRef, _ := cmd.Flags().GetString("token")
	command, _ := cmd.Flags().GetString("command")
	username, _ := cmd.Flags().GetString("username")
	groupname, _ := cmd.Flags().GetString("groupname")
	serverRefs, _ := cmd.Flags().GetStringSlice("server")
	expiresAt, _ := cmd.Flags().GetString("expires-at")
	disabled, _ := cmd.Flags().GetBool("disabled")

	tokenRef, command, username = strings.TrimSpace(tokenRef), strings.TrimSpace(command), strings.TrimSpace(username)
	for _, required := range []struct{ flag, value string }{{"token", tokenRef}, {"command", command}, {"username", username}} {
		if required.value == "" {
			utils.CliUsageErrorWithExit("--%s is required (or pass --file).", required.flag)
		}
	}
	if err := validateExpiry(expiresAt); err != nil {
		utils.CliUsageErrorWithExit("%s", err)
	}
	if err := validateServerRefs(cmd.Flags().Changed("server"), serverRefs); err != nil {
		utils.CliUsageErrorWithExit("%s", err)
	}

	return &flagEntry{
		tokenRef:   tokenRef,
		serverRefs: serverRefs,
		request: acl.EntryRequest{
			CommandPattern: command,
			Username:       username,
			Groupname:      groupname,
			ExpiresAt:      expiresAt,
			Enabled:        !disabled,
		},
	}
}

// validateServerRefs refuses a --server given with nothing in it, such as
// --server "" or a stray comma, which would otherwise cover every server.
func validateServerRefs(given bool, refs []string) error {
	if given && len(refs) == 0 {
		return errEmptyServerScope
	}
	_, _, err := partitionServerRefs(refs)
	return err
}

// resolve names the service token and servers by the ids the ledger stores,
// looked up in the workspace the session was opened for.
func (f *flagEntry) resolve(session *ledgerSession) acl.EntryRequest {
	principalID, err := session.resolveServiceToken(f.tokenRef)
	if err != nil {
		utils.CliErrorWithExit("Failed to find service token %q: %s.", f.tokenRef, err)
	}
	serverIDs, err := session.resolveServerScope(f.serverRefs)
	if err != nil {
		utils.CliErrorWithExit("Failed to resolve the entry's servers: %s.", err)
	}

	request := f.request
	request.PrincipalID = principalID
	request.ServerIDs = serverIDs
	return request
}

// confirmServiceTokens checks every --file principal_id against the session's
// workspace before anything is issued, so none can be a personal API token's.
func (s *ledgerSession) confirmServiceTokens(entries []acl.EntryRequest) {
	confirmed := map[string]bool{}
	for i, entry := range entries {
		if confirmed[entry.PrincipalID] {
			continue
		}
		if _, err := s.resolveServiceToken(entry.PrincipalID); err != nil {
			utils.CliErrorWithExit("Entry %d: failed to find service token %s: %s.", i, entry.PrincipalID, err)
		}
		confirmed[entry.PrincipalID] = true
	}
}

// validateExpiry refuses an unreadable time and one not in the future, which
// would issue an entry that never matches.
func validateExpiry(expiresAt string) error {
	if expiresAt == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return fmt.Errorf("--expires-at must be an RFC 3339 time such as 2027-01-15T10:00:00Z, not %q", expiresAt)
	}
	if !parsed.After(now()) {
		return fmt.Errorf("--expires-at %s is not in the future", expiresAt)
	}
	return nil
}

// fileEntry is one item of an --file batch. principal_type is read only to
// refuse anything but a service token; an omitted enabled issues it enabled.
type fileEntry struct {
	PrincipalType  string    `json:"principal_type"`
	PrincipalID    string    `json:"principal_id"`
	CommandPattern string    `json:"command_pattern"`
	Username       string    `json:"username"`
	Groupname      string    `json:"groupname"`
	ServerIDs      *[]string `json:"server_ids"`
	ExpiresAt      string    `json:"expires_at"`
	Enabled        *bool     `json:"enabled"`
}

// validate refuses what the flags would refuse. server_ids is required, so
// covering every server is written as [] on purpose, never by omission.
func (item *fileEntry) validate() error {
	item.PrincipalID = strings.TrimSpace(item.PrincipalID)
	item.CommandPattern = strings.TrimSpace(item.CommandPattern)
	item.Username = strings.TrimSpace(item.Username)
	switch {
	case item.PrincipalType != "" && item.PrincipalType != acl.PrincipalTypeServiceToken:
		return fmt.Errorf("principal_type must be %q; a personal API token has no command list on the ledger", acl.PrincipalTypeServiceToken)
	case !utils.IsUUID(item.PrincipalID):
		return errors.New("principal_id must be a service token ID")
	case item.CommandPattern == "":
		return errors.New("command_pattern is required")
	case item.Username == "":
		return errors.New(`username is required ("*" for any account)`)
	case item.ServerIDs == nil:
		return errors.New("server_ids is required; use [] to cover every server")
	}
	for _, id := range *item.ServerIDs {
		if strings.TrimSpace(id) == "" {
			return errEmptyServerScope
		}
	}
	return validateExpiry(item.ExpiresAt)
}

// parseEntryFile reads an --file batch. Unknown fields are refused, so a
// misspelled one is not silently dropped and the entry issued wider than meant.
func parseEntryFile(data []byte) ([]acl.EntryRequest, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var items []fileEntry
	if err := decoder.Decode(&items); err != nil {
		return nil, fmt.Errorf("expected a JSON array of entries: %w", err)
	}
	// One array and nothing after it: the batch is all or nothing, so a second
	// value concatenated onto the file must not be ignored.
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("expected a single JSON array of entries, but the file continues after it")
	}
	if len(items) == 0 {
		return nil, errors.New("the file holds no entries")
	}

	entries := make([]acl.EntryRequest, 0, len(items))
	for i, item := range items {
		if err := item.validate(); err != nil {
			return nil, fmt.Errorf("entry %d: %w", i, err)
		}
		entries = append(entries, acl.EntryRequest{
			PrincipalID:    item.PrincipalID,
			CommandPattern: item.CommandPattern,
			Username:       item.Username,
			Groupname:      item.Groupname,
			ServerIDs:      *item.ServerIDs,
			ExpiresAt:      item.ExpiresAt,
			Enabled:        item.Enabled == nil || *item.Enabled,
		})
	}
	return entries, nil
}
