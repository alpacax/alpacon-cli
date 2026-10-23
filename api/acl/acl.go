// Package acl talks to the command pre-approval ledger held by the signer.
package acl

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	capabilityURL      = "/api/acl/me/"
	entryURL           = "/api/acl/entries/"
	entryBulkURL       = "/api/acl/entries/bulk/"
	entryBulkDeleteURL = "/api/acl/entries/bulk/delete/"
	adminURL           = "/api/acl/admins/"
)

// GetCapability asks the signer whether the caller is a Command ACL admin, and
// of which workspaces. It answers 200 for any person, including one who
// administers nothing, so a refusal elsewhere can be told apart from that.
func GetCapability(ac *client.AlpaconClient) (Capability, error) {
	body, err := ac.SendGetRequest(capabilityURL)
	if err != nil {
		return Capability{}, err
	}
	var capability Capability
	if err = json.Unmarshal(body, &capability); err != nil {
		return Capability{}, err
	}
	return capability, nil
}

// ListEntries returns the workspace's active entries, narrowed to one service
// token when principalID is set. The signer refuses a list that names no
// workspace rather than spanning every workspace the caller administers.
func ListEntries(ac *client.AlpaconClient, workspaceID, principalID string) ([]EntryResponse, error) {
	params := map[string]string{"workspace_id": workspaceID}
	if principalID != "" {
		params["principal_type"] = PrincipalTypeServiceToken
		params["principal_id"] = principalID
	}
	return api.FetchAllPages[EntryResponse](ac, entryURL, params)
}

// IssueEntries issues every entry or none: the bulk route refuses the whole
// batch on one bad row, and it is used for a single entry too so both meet the
// same duplicate refusal.
//
// Only service tokens are issued entries. The ledger keeps api_token entries
// for tokens that held them before, but the web console neither issues nor
// shows them on a personal API token's page, so one issued here could not be
// seen or revoked there. The refusal happens before any request is sent.
func IssueEntries(ac *client.AlpaconClient, workspaceID string, entries []EntryRequest) ([]EntryResponse, error) {
	for i, entry := range entries {
		if entry.PrincipalType != PrincipalTypeServiceToken {
			return nil, fmt.Errorf("entry %d names principal type %q: only service tokens can be issued pre-approved commands, and a personal API token has no command list on the ledger", i, entry.PrincipalType)
		}
	}
	for i := range entries {
		// The ledger reads an empty scope as every server in the workspace; a
		// nil slice would marshal as null, which is not a list at all.
		if entries[i].ServerIDs == nil {
			entries[i].ServerIDs = []string{}
		}
	}

	body, err := ac.SendPostRequest(entryBulkURL, bulkIssueRequest{WorkspaceID: workspaceID, Entries: entries})
	if err != nil {
		return nil, err
	}
	var issued []EntryResponse
	if err = json.Unmarshal(body, &issued); err != nil {
		return nil, err
	}
	return issued, nil
}

// RevokeEntry withdraws one entry. The signer keeps the row readable by id for
// the audit trail; it stops matching at once.
func RevokeEntry(ac *client.AlpaconClient, entryID string) error {
	pathID, err := api.RequireID(entryID, "entry")
	if err != nil {
		return err
	}
	_, err = ac.SendDeleteRequest(api.IDURL(entryURL, pathID, "", nil))
	return err
}

// RevokeEntries withdraws every entry in entryIDs or none, returning how many
// the signer revoked.
func RevokeEntries(ac *client.AlpaconClient, entryIDs []string) (int, error) {
	body, err := ac.SendPostRequest(entryBulkDeleteURL, bulkRevokeRequest{IDs: entryIDs})
	if err != nil {
		return 0, err
	}
	var response bulkRevokeResponse
	if err = json.Unmarshal(body, &response); err != nil {
		return 0, err
	}
	return response.Revoked, nil
}

// SetEntryEnabled turns an entry off or back on without withdrawing it.
func SetEntryEnabled(ac *client.AlpaconClient, entryID string, enabled bool) (EntryResponse, error) {
	pathID, err := api.RequireID(entryID, "entry")
	if err != nil {
		return EntryResponse{}, err
	}
	url := api.IDURL(entryURL, pathID, "disable", nil)
	if enabled {
		url = api.IDURL(entryURL, pathID, "enable", nil)
	}

	// The route takes no body; an empty object is the body that says so.
	body, err := ac.SendPostRequest(url, struct{}{})
	if err != nil {
		return EntryResponse{}, err
	}
	var entry EntryResponse
	if err = json.Unmarshal(body, &entry); err != nil {
		return EntryResponse{}, err
	}
	return entry, nil
}

// ListAdmins returns the workspace's active Command ACL admins.
func ListAdmins(ac *client.AlpaconClient, workspaceID string) ([]AdminResponse, error) {
	return api.FetchAllPages[AdminResponse](ac, adminURL, map[string]string{"workspace_id": workspaceID})
}

// GrantAdmin makes subject a Command ACL admin of the workspace. email is
// recorded for people to read; the subject is what authorizes.
func GrantAdmin(ac *client.AlpaconClient, workspaceID, subject, email string) (AdminResponse, error) {
	body, err := ac.SendPostRequest(adminURL, adminGrantRequest{WorkspaceID: workspaceID, Subject: subject, Email: email})
	if err != nil {
		return AdminResponse{}, err
	}
	var admin AdminResponse
	if err = json.Unmarshal(body, &admin); err != nil {
		return AdminResponse{}, err
	}
	return admin, nil
}

// RevokeAdmin withdraws one admin grant by its id.
func RevokeAdmin(ac *client.AlpaconClient, adminID string) error {
	pathID, err := api.RequireResponseID(adminID, "admin grant")
	if err != nil {
		return err
	}
	_, err = ac.SendDeleteRequest(api.IDURL(adminURL, pathID, "", nil))
	return err
}

// FindAdmin picks the grant ref names among one workspace's admins: a grant id,
// an Auth0 subject, or an email. An email two grants share is refused rather
// than resolved to the first, since revoking the wrong person is not something
// the caller could see afterwards.
func FindAdmin(admins []AdminResponse, ref string) (AdminResponse, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return AdminResponse{}, fmt.Errorf("an admin's subject, email, or grant id is required")
	}

	var byEmail []AdminResponse
	for _, admin := range admins {
		if admin.ID == ref || admin.Subject == ref {
			return admin, nil
		}
		if admin.Email != "" && strings.EqualFold(admin.Email, ref) {
			byEmail = append(byEmail, admin)
		}
	}

	switch len(byEmail) {
	case 0:
		return AdminResponse{}, fmt.Errorf("no Command ACL admin of this workspace matches %q", ref)
	case 1:
		return byEmail[0], nil
	default:
		return AdminResponse{}, fmt.Errorf("%d Command ACL admins share the email %q; name one by its subject instead", len(byEmail), ref)
	}
}

// ToEntryAttributes projects entries for a table. tokenNames and serverNames map
// ids to names where the workspace could supply them; an id with no name is
// shown as it is.
func ToEntryAttributes(entries []EntryResponse, tokenNames, serverNames map[string]string) []EntryAttributes {
	attributes := make([]EntryAttributes, 0, len(entries))
	for _, entry := range entries {
		principal := nameOr(tokenNames, entry.PrincipalID)
		if entry.PrincipalType != PrincipalTypeServiceToken {
			principal = fmt.Sprintf("%s (%s)", principal, entry.PrincipalType)
		}
		attributes = append(attributes, EntryAttributes{
			ID:        entry.ID,
			Principal: principal,
			Command:   entry.CommandPattern,
			Username:  entry.Username,
			Groupname: entry.Groupname,
			Servers:   FormatServerScope(entry.ServerIDs, serverNames),
			Enabled:   entry.Enabled,
			ExpiresAt: formatExpiry(entry.ExpiresAt),
			CreatedBy: entry.CreatedByEmail,
		})
	}
	return attributes
}

func ToAdminAttributes(admins []AdminResponse) []AdminAttributes {
	attributes := make([]AdminAttributes, 0, len(admins))
	for _, admin := range admins {
		attributes = append(attributes, AdminAttributes{
			ID:        admin.ID,
			Subject:   admin.Subject,
			Email:     admin.Email,
			GrantedBy: admin.GrantedBySubject,
			CreatedAt: formatTime(admin.CreatedAt),
		})
	}
	return attributes
}

// FormatServerScope renders an entry's server scope. An empty list is not "no
// servers"—it is every server in the workspace.
func FormatServerScope(serverIDs []string, serverNames map[string]string) string {
	if len(serverIDs) == 0 {
		return "all servers"
	}
	names := make([]string, 0, len(serverIDs))
	for _, id := range serverIDs {
		names = append(names, nameOr(serverNames, id))
	}
	return strings.Join(names, ", ")
}

func nameOr(names map[string]string, id string) string {
	if name := names[id]; name != "" {
		return name
	}
	return id
}

func formatExpiry(expiresAt *string) string {
	if expiresAt == nil || *expiresAt == "" {
		return "never"
	}
	return formatTime(*expiresAt)
}

func formatTime(value string) string {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	return utils.TimeUtils(parsed)
}
