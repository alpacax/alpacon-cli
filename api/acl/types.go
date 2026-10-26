package acl

import (
	"slices"
	"strings"
)

// PrincipalTypeServiceToken is the only principal type IssueEntries issues;
// api_token entries already on the ledger are still listed and revocable.
const PrincipalTypeServiceToken = "service_token"

// Capability is the signer's answer to what the caller may do with the ledger.
type Capability struct {
	IsLedgerAdmin bool     `json:"is_ledger_admin"`
	WorkspaceIDs  []string `json:"workspace_ids"`
}

// Administers reports whether the caller is a Command ACL admin of workspaceID:
// the flag and the listed workspace must agree.
func (c Capability) Administers(workspaceID string) bool {
	return c.IsLedgerAdmin && slices.ContainsFunc(c.WorkspaceIDs, func(id string) bool { return strings.EqualFold(id, workspaceID) })
}

type EntryResponse struct {
	ID             string   `json:"id"`
	WorkspaceID    string   `json:"workspace_id"`
	PrincipalType  string   `json:"principal_type"`
	PrincipalID    string   `json:"principal_id"`
	CommandPattern string   `json:"command_pattern"`
	Username       string   `json:"username"`
	Groupname      string   `json:"groupname"`
	ServerIDs      []string `json:"server_ids"`
	EntryKind      string   `json:"entry_kind"`
	ExpiresAt      *string  `json:"expires_at"`
	Enabled        bool     `json:"enabled"`
	CreatedByEmail string   `json:"created_by_email"`
	CreatedAt      string   `json:"created_at"`
	// Warnings carries the signer's write-time advisories, such as an entry
	// with no server scope applying to every server in the workspace.
	Warnings []string `json:"warnings"`
}

type EntryAttributes struct {
	ID        string `json:"id" table:"ID"`
	Principal string `json:"principal" table:"Service token"`
	Command   string `json:"command_pattern" table:"Command"`
	Username  string `json:"username" table:"User"`
	Groupname string `json:"groupname" table:"Group"`
	Servers   string `json:"servers" table:"Servers"`
	Enabled   bool   `json:"enabled" table:"Enabled"`
	ExpiresAt string `json:"expires_at" table:"Expires at"`
	CreatedBy string `json:"created_by" table:"Created by"`
}

// EntryRequest is one entry to issue, for the service token PrincipalID names.
// IssueEntries states the principal type on the wire.
type EntryRequest struct {
	PrincipalID    string   `json:"principal_id"`
	CommandPattern string   `json:"command_pattern"`
	Username       string   `json:"username"`
	Groupname      string   `json:"groupname,omitempty"`
	ServerIDs      []string `json:"server_ids"`
	ExpiresAt      string   `json:"expires_at,omitempty"`
	Enabled        bool     `json:"enabled"`
}

// wireEntry is an EntryRequest as sent, with the principal type it can only be.
type wireEntry struct {
	PrincipalType string `json:"principal_type"`
	EntryRequest
}

type bulkIssueRequest struct {
	WorkspaceID string      `json:"workspace_id"`
	Entries     []wireEntry `json:"entries"`
}

type bulkRevokeRequest struct {
	IDs []string `json:"ids"`
}

type bulkRevokeResponse struct {
	Revoked int `json:"revoked"`
}

type AdminResponse struct {
	ID               string `json:"id"`
	WorkspaceID      string `json:"workspace_id"`
	Subject          string `json:"subject"`
	Email            string `json:"email"`
	GrantedBySubject string `json:"granted_by_subject"`
	CreatedAt        string `json:"created_at"`
}

type AdminAttributes struct {
	ID        string `json:"id" table:"ID"`
	Subject   string `json:"subject" table:"Subject"`
	Email     string `json:"email" table:"Email"`
	GrantedBy string `json:"granted_by" table:"Granted by"`
	CreatedAt string `json:"created_at" table:"Granted at"`
}

type adminGrantRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Subject     string `json:"subject"`
	Email       string `json:"email,omitempty"`
}
