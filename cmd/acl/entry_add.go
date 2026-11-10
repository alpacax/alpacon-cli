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
	"github.com/alpacax/alpacon-cli/api/auth"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var entryAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Pre-approve a command for a service token",
	Long: `Pre-approve a command pattern for a service token.

Use * as a wildcard for arguments; a pattern without one pre-approves that exact
command line.

For a service token, omitting --username pins the entry to the application's own
account. In --username, '' or * matches any account bound to the application on
that server; the application username matches only its own account. In
--groupname, '' or * sets no group restriction; the application username matches
its own group. Any other value is refused.
Without --server the entry covers every server in the workspace, and the
command signer warns about it.

--file issues several entries at once from a JSON array whose items carry the
ledger's own fields: principal_id (a service token ID), command_pattern,
server_ids ([] covers every server), and optionally username, groupname,
expires_at and enabled. Only an absent username defaults to the application
username; an explicit empty username is sent as given. The batch is all or
nothing: one refused entry refuses the whole file. principal_type may be left
out; it can only be service_token.

Only a service token can be pre-approved commands: a personal API token has no
command list on the ledger. Existing entries for personal API tokens are still
listed and can be revoked.`,
	Example: `  alpacon acl entry add --token deploy-bot --command "systemctl restart nginx" \
    --server web-01

  alpacon acl entry add --token deploy-bot --command "docker compose *" \
    --username deploy --server web-01,web-02 --expires-at 2027-01-15T10:00:00Z

  alpacon acl entry add --file entries.json`,
	Args: cobra.NoArgs,
	Run:  runEntryAdd,
}

func init() {
	entryAddCmd.Flags().String("token", "", "Service token the entry pre-approves the command for (name or ID)")
	entryAddCmd.Flags().StringP("command", "c", "", "Command pattern to pre-approve (supports * wildcard)")
	entryAddCmd.Flags().String("username", "", "Omit to pin the entry to the application's own account; '' or * matches any account bound to the application on that server; the application username matches only its own account; any other value is refused")
	entryAddCmd.Flags().String("groupname", "", "'' or * sets no group restriction; the application username matches its own group; any other value is refused")
	entryAddCmd.Flags().StringSlice("server", nil, "Servers the entry covers, by name or ID (repeatable or comma-separated; default: every server)")
	entryAddCmd.Flags().String("expires-at", "", "Expiry time in RFC 3339 (default: no expiry)")
	entryAddCmd.Flags().Bool("disabled", false, "Issue the entry disabled")
	entryAddCmd.Flags().String("file", "", "Issue every entry in this JSON file")
}

func runEntryAdd(cmd *cobra.Command, args []string) {
	file, _ := cmd.Flags().GetString("file")

	var drafts []entryDraft
	var flagged *flagEntry
	if file != "" {
		if flagged := entryFlagsSet(cmd); len(flagged) > 0 {
			utils.CliUsageErrorWithExit("--file cannot be combined with --%s.", flagged[0])
		}
		data, err := os.ReadFile(file)
		if err != nil {
			utils.CliErrorWithExit("Failed to read %s: %s.", file, err)
		}
		if drafts, err = parseEntryFile(data); err != nil {
			utils.CliErrorWithExit("Failed to read %s: %s.", file, err)
		}
	} else {
		flagged = entryFromFlags(cmd)
	}

	session, err := newLedgerSessionWithCheck(func(s *ledgerSession) error {
		if flagged != nil {
			request, err := flagged.resolve(s)
			if err != nil {
				return err
			}
			drafts = []entryDraft{{EntryRequest: request, usernameOmitted: !cmd.Flags().Changed("username")}}
		}
		return s.bindApplicationAccounts(drafts, file != "")
	})
	if err != nil {
		var accountErr serviceTokenAccountError
		if errors.As(err, &accountErr) {
			utils.CliErrorWithCode(utils.ExitCodeUsageError, utils.ServiceTokenAccountNotOwn,
				"%s", utils.Reword(err, sentence(upperFirst(err.Error()))))
		}
		utils.CliErrorWithExit("%s", utils.Reword(err, sentence(upperFirst(err.Error()))))
	}
	entries := make([]acl.EntryRequest, len(drafts))
	for i, draft := range drafts {
		entries[i] = draft.EntryRequest
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
	for _, required := range []struct{ flag, value string }{{"token", tokenRef}, {"command", command}} {
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

// resolve looks up the server scope in the session's workspace.
func (f *flagEntry) resolve(session *ledgerSession) (acl.EntryRequest, error) {
	serverIDs, err := session.resolveServerScope(f.serverRefs)
	if err != nil {
		return acl.EntryRequest{}, fmt.Errorf("failed to resolve the entry's servers: %w", err)
	}
	request := f.request
	request.PrincipalID = f.tokenRef
	request.ServerIDs = serverIDs
	return request, nil
}

type entryDraft struct {
	acl.EntryRequest
	usernameOmitted bool
}

type serviceTokenAccountError struct{ message string }

func (e serviceTokenAccountError) Error() string       { return e.message }
func (e serviceTokenAccountError) ErrorCode() string   { return utils.ServiceTokenAccountNotOwn }
func (e serviceTokenAccountError) ErrorSource() string { return "" }

// bindApplicationAccounts confirms every principal and defaults omitted usernames before a signer write.
func (s *ledgerSession) bindApplicationAccounts(entries []entryDraft, fileMode bool) error {
	ac, err := s.workspaceAPI()
	if err != nil {
		return err
	}
	confirmed := map[string]auth.ServiceToken{}
	for i := range entries {
		entry := &entries[i]
		prefix := ""
		if fileMode {
			prefix = fmt.Sprintf("entry %d: ", i)
		}
		token, ok := confirmed[entry.PrincipalID]
		if !ok {
			token, err = auth.ResolveServiceToken(ac, entry.PrincipalID)
			if err != nil {
				return fmt.Errorf("%sfailed to find service token: %w", prefix, err)
			}
			confirmed[entry.PrincipalID] = token
		}
		if token.Application == nil || token.Application.Username == "" {
			return fmt.Errorf("%sthe workspace did not return the service token's application username", prefix)
		}
		username := token.Application.Username
		if !accountMatches(entry.Username, username) {
			return serviceTokenAccountError{fmt.Sprintf("%susername: the account must be empty, * or %q (code: service_token_account_not_own)", prefix, username)}
		}
		if !accountMatches(entry.Groupname, username) {
			return serviceTokenAccountError{fmt.Sprintf("%sgroupname: the group must be empty, * or %q (code: service_token_account_not_own)", prefix, username)}
		}
		entry.PrincipalID = token.ID
		if entry.usernameOmitted {
			entry.Username = username
		}
	}
	return nil
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
	PrincipalType  string          `json:"principal_type"`
	PrincipalID    string          `json:"principal_id"`
	CommandPattern string          `json:"command_pattern"`
	Username       json.RawMessage `json:"username"`
	Groupname      json.RawMessage `json:"groupname"`
	ServerIDs      *[]string       `json:"server_ids"`
	ExpiresAt      string          `json:"expires_at"`
	Enabled        *bool           `json:"enabled"`
}

// validate refuses what the flags would refuse. server_ids is required, so
// covering every server is written as [] on purpose, never by omission.
func (item *fileEntry) validate() error {
	item.PrincipalID = strings.TrimSpace(item.PrincipalID)
	item.CommandPattern = strings.TrimSpace(item.CommandPattern)
	switch {
	case item.PrincipalType != "" && item.PrincipalType != acl.PrincipalTypeServiceToken:
		return fmt.Errorf("principal_type must be %q; a personal API token has no command list on the ledger", acl.PrincipalTypeServiceToken)
	case !utils.IsUUID(item.PrincipalID):
		return errors.New("principal_id must be a service token ID")
	case item.CommandPattern == "":
		return errors.New("command_pattern is required")
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
func parseEntryFile(data []byte) ([]entryDraft, error) {
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

	entries := make([]entryDraft, 0, len(items))
	for i, item := range items {
		if err := item.validate(); err != nil {
			return nil, fmt.Errorf("entry %d: %w", i, err)
		}
		username, err := parseAccountField(item.Username, i, "username")
		if err != nil {
			return nil, err
		}
		groupname, err := parseAccountField(item.Groupname, i, "groupname")
		if err != nil {
			return nil, err
		}
		entries = append(entries, entryDraft{EntryRequest: acl.EntryRequest{
			PrincipalID:    item.PrincipalID,
			CommandPattern: item.CommandPattern,
			Username:       username,
			Groupname:      groupname,
			ServerIDs:      *item.ServerIDs,
			ExpiresAt:      item.ExpiresAt,
			Enabled:        item.Enabled == nil || *item.Enabled,
		}, usernameOmitted: item.Username == nil})
	}
	return entries, nil
}

func parseAccountField(data json.RawMessage, index int, field string) (string, error) {
	if data == nil {
		return "", nil
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return "", utils.MarkUsageError(fmt.Errorf("entry %d: %s must be a string, not null", index, field))
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return "", utils.MarkUsageError(fmt.Errorf("entry %d: %s: %w", index, field, err))
	}
	return strings.TrimSpace(value), nil
}
