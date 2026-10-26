package acl

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testWorkspaceID = "8b0f2c8e-1d4a-4c3b-9a55-0c1d2e3f4a5b"
	testTokenID     = "3f6c1b2a-4d5e-4f60-8a7b-9c0d1e2f3a4b"
	testEntryID     = "550e8400-e29b-41d4-a716-446655440000"
	testServerID    = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
)

// recordedRequest is what the stub signer saw of one request.
type recordedRequest struct {
	Method string
	Path   string
	Query  map[string]string
	Body   string
	Auth   string
}

// stubSigner answers every request with respond and records what it was sent.
type stubSigner struct {
	mu       sync.Mutex
	requests []recordedRequest
}

func (s *stubSigner) recorded() []recordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedRequest(nil), s.requests...)
}

func newStubSigner(t *testing.T, respond func(r *http.Request) (int, string)) (*client.AlpaconClient, *stubSigner) {
	t.Helper()
	stub := &stubSigner{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		query := map[string]string{}
		for key := range r.URL.Query() {
			query[key] = r.URL.Query().Get(key)
		}
		stub.mu.Lock()
		stub.requests = append(stub.requests, recordedRequest{
			Method: r.Method, Path: r.URL.Path, Query: query, Body: string(body), Auth: r.Header.Get("Authorization"),
		})
		stub.mu.Unlock()

		status, payload := respond(r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, payload)
	}))
	t.Cleanup(ts.Close)

	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}
	ac.SetAccessToken("signer-token")
	return ac, stub
}

func ok(payload string) func(*http.Request) (int, string) {
	return func(*http.Request) (int, string) { return http.StatusOK, payload }
}

func TestGetCapability(t *testing.T) {
	t.Parallel()
	ac, stub := newStubSigner(t, ok(`{"is_ledger_admin": true, "workspace_ids": ["`+testWorkspaceID+`"]}`))

	capability, err := GetCapability(ac)

	require.NoError(t, err)
	assert.True(t, capability.IsLedgerAdmin)
	assert.True(t, capability.Administers(testWorkspaceID))
	assert.True(t, capability.Administers(strings.ToUpper(testWorkspaceID)), "a UUID compares case-insensitively")
	assert.False(t, capability.Administers("00000000-0000-0000-0000-000000000000"))
	requests := stub.recorded()
	require.Len(t, requests, 1)
	assert.Equal(t, "/api/acl/me/", requests[0].Path)
	assert.Equal(t, "Bearer signer-token", requests[0].Auth)
}

func TestListEntries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		principalID string
		wantQuery   map[string]string
	}{
		{name: "whole workspace", wantQuery: map[string]string{"workspace_id": testWorkspaceID, "page": "1", "page_size": "100"}},
		{name: "one service token", principalID: testTokenID, wantQuery: map[string]string{
			"workspace_id": testWorkspaceID, "page": "1", "page_size": "100",
			"principal_type": "service_token", "principal_id": testTokenID,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ac, stub := newStubSigner(t, ok(`{"count": 1, "current": 1, "next": null, "previous": null, "last": 1, "results": [{"id": "`+testEntryID+`", "command_pattern": "uptime"}]}`))

			entries, err := ListEntries(ac, testWorkspaceID, tt.principalID)

			require.NoError(t, err)
			require.Len(t, entries, 1)
			assert.Equal(t, "uptime", entries[0].CommandPattern)
			requests := stub.recorded()
			require.Len(t, requests, 1)
			assert.Equal(t, "/api/acl/entries/", requests[0].Path)
			assert.Equal(t, tt.wantQuery, requests[0].Query)
		})
	}
}

// The signer pages by number, as the workspace API does, so the shared walk
// follows it to the last page.
func TestListEntries_WalksEveryPage(t *testing.T) {
	t.Parallel()
	ac, stub := newStubSigner(t, func(r *http.Request) (int, string) {
		if r.URL.Query().Get("page") == "1" {
			return http.StatusOK, `{"count": 2, "current": 1, "next": 2, "previous": null, "last": 2, "results": [{"id": "a"}]}`
		}
		return http.StatusOK, `{"count": 2, "current": 2, "next": null, "previous": 1, "last": 2, "results": [{"id": "b"}]}`
	})

	entries, err := ListEntries(ac, testWorkspaceID, "")

	require.NoError(t, err)
	assert.Len(t, entries, 2)
	assert.Len(t, stub.recorded(), 2)
}

func TestIssueEntries_SendsOneEnvelope(t *testing.T) {
	t.Parallel()
	ac, stub := newStubSigner(t, func(*http.Request) (int, string) {
		return http.StatusCreated, `[{"id": "` + testEntryID + `", "command_pattern": "uptime", "warnings": ["unscoped"]}]`
	})

	issued, err := IssueEntries(ac, testWorkspaceID, []EntryRequest{{
		PrincipalID:    testTokenID,
		CommandPattern: "uptime", Username: "*", Enabled: true,
	}})

	require.NoError(t, err)
	require.Len(t, issued, 1)
	assert.Equal(t, []string{"unscoped"}, issued[0].Warnings)
	requests := stub.recorded()
	require.Len(t, requests, 1)
	assert.Equal(t, http.MethodPost, requests[0].Method)
	assert.Equal(t, "/api/acl/entries/bulk/", requests[0].Path)
	// An unscoped entry sends an empty list, never null.
	assert.JSONEq(t, `{"workspace_id": "`+testWorkspaceID+`", "entries": [{
		"principal_type": "service_token", "principal_id": "`+testTokenID+`",
		"command_pattern": "uptime", "username": "*", "server_ids": [], "enabled": true
	}]}`, requests[0].Body)
}

func TestIssueEntries_RelaysTheSignersRefusal(t *testing.T) {
	t.Parallel()
	ac, _ := newStubSigner(t, func(*http.Request) (int, string) {
		return http.StatusBadRequest, `{"entries": ["Row 0 is a grant the ledger already holds: 'uptime' as '*'. Revoke it first to reissue."], "code": "duplicate_entry"}`
	})

	_, err := IssueEntries(ac, testWorkspaceID, []EntryRequest{{PrincipalID: testTokenID, CommandPattern: "uptime", Username: "*"}})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "already holds")
	assert.Equal(t, http.StatusBadRequest, utils.HTTPStatusCode(err))
}

func TestRevokeEntry(t *testing.T) {
	t.Parallel()
	ac, stub := newStubSigner(t, func(*http.Request) (int, string) { return http.StatusNoContent, "" })

	require.NoError(t, RevokeEntry(ac, testEntryID))

	requests := stub.recorded()
	require.Len(t, requests, 1)
	assert.Equal(t, http.MethodDelete, requests[0].Method)
	assert.Equal(t, "/api/acl/entries/"+testEntryID+"/", requests[0].Path)
}

func TestByIDCalls_RefuseAnIDThatIsNotOneSegment(t *testing.T) {
	t.Parallel()
	const unsafe = "../admins/" + testEntryID
	calls := []struct {
		name string
		call func(ac *client.AlpaconClient) error
	}{
		{name: "get", call: func(ac *client.AlpaconClient) error { _, err := GetEntry(ac, unsafe); return err }},
		{name: "revoke", call: func(ac *client.AlpaconClient) error { return RevokeEntry(ac, unsafe) }},
		{name: "enable", call: func(ac *client.AlpaconClient) error { _, err := SetEntryEnabled(ac, unsafe, true); return err }},
	}
	for _, tt := range calls {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ac, stub := newStubSigner(t, func(*http.Request) (int, string) { return http.StatusNoContent, "" })

			require.ErrorIs(t, tt.call(ac), utils.ErrInvalidID)
			assert.Empty(t, stub.recorded())
		})
	}
}

// An admin grant id comes from the signer's own list, so a malformed one is the
// server's fault rather than the caller's.
func TestRevokeAdmin_RefusesAnUnusableGrantID(t *testing.T) {
	t.Parallel()
	ac, stub := newStubSigner(t, func(*http.Request) (int, string) { return http.StatusNoContent, "" })

	require.ErrorIs(t, RevokeAdmin(ac, "../entries"), api.ErrUnusableID)
	assert.Empty(t, stub.recorded())
}

func TestGetEntry(t *testing.T) {
	t.Parallel()
	ac, stub := newStubSigner(t, ok(`{"id": "`+testEntryID+`", "workspace_id": "`+testWorkspaceID+`", "command_pattern": "uptime"}`))

	entry, err := GetEntry(ac, testEntryID)

	require.NoError(t, err)
	assert.Equal(t, testWorkspaceID, entry.WorkspaceID)
	requests := stub.recorded()
	require.Len(t, requests, 1)
	assert.Equal(t, http.MethodGet, requests[0].Method)
	assert.Equal(t, "/api/acl/entries/"+testEntryID+"/", requests[0].Path)
}

func TestRevokeEntries(t *testing.T) {
	t.Parallel()
	ac, stub := newStubSigner(t, ok(`{"revoked": 2}`))

	revoked, err := RevokeEntries(ac, []string{testEntryID, testServerID})

	require.NoError(t, err)
	assert.Equal(t, 2, revoked)
	requests := stub.recorded()
	require.Len(t, requests, 1)
	assert.Equal(t, "/api/acl/entries/bulk/delete/", requests[0].Path)
	assert.JSONEq(t, `{"ids": ["`+testEntryID+`", "`+testServerID+`"]}`, requests[0].Body)
}

func TestSetEntryEnabled(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		enabled  bool
		wantPath string
	}{
		{name: "disable", enabled: false, wantPath: "/api/acl/entries/" + testEntryID + "/disable/"},
		{name: "enable", enabled: true, wantPath: "/api/acl/entries/" + testEntryID + "/enable/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ac, stub := newStubSigner(t, ok(`{"id": "`+testEntryID+`", "command_pattern": "uptime"}`))

			entry, err := SetEntryEnabled(ac, testEntryID, tt.enabled)

			require.NoError(t, err)
			assert.Equal(t, "uptime", entry.CommandPattern)
			requests := stub.recorded()
			require.Len(t, requests, 1)
			assert.Equal(t, http.MethodPost, requests[0].Method)
			assert.Equal(t, tt.wantPath, requests[0].Path)
		})
	}
}

func TestAdmins(t *testing.T) {
	t.Parallel()
	ac, stub := newStubSigner(t, func(r *http.Request) (int, string) {
		switch r.Method {
		case http.MethodGet:
			return http.StatusOK, `{"count": 1, "current": 1, "next": null, "last": 1, "results": [{"id": "grant-1", "subject": "auth0|alice"}]}`
		case http.MethodPost:
			return http.StatusCreated, `{"id": "grant-2", "subject": "auth0|bob"}`
		default:
			return http.StatusNoContent, ""
		}
	})

	admins, err := ListAdmins(ac, testWorkspaceID)
	require.NoError(t, err)
	require.Len(t, admins, 1)

	granted, err := GrantAdmin(ac, testWorkspaceID, "auth0|bob", "bob@example.com")
	require.NoError(t, err)
	assert.Equal(t, "grant-2", granted.ID)

	require.NoError(t, RevokeAdmin(ac, "grant-1"))

	requests := stub.recorded()
	require.Len(t, requests, 3)
	assert.Equal(t, testWorkspaceID, requests[0].Query["workspace_id"])
	assert.Equal(t, "/api/acl/admins/", requests[1].Path)
	assert.JSONEq(t, `{"workspace_id": "`+testWorkspaceID+`", "subject": "auth0|bob", "email": "bob@example.com"}`, requests[1].Body)
	assert.Equal(t, http.MethodDelete, requests[2].Method)
	assert.Equal(t, "/api/acl/admins/grant-1/", requests[2].Path)
}

// The last-admin guard is the signer's to enforce; the CLI relays its words.
func TestRevokeAdmin_RelaysTheLastAdminRefusal(t *testing.T) {
	t.Parallel()
	ac, _ := newStubSigner(t, func(*http.Request) (int, string) {
		return http.StatusBadRequest, `{"subject": ["This is the last admin of the workspace. Removing it would leave the ledger reachable only by an operator with shell access; grant another admin first."], "code": "last_admin"}`
	})

	err := RevokeAdmin(ac, "grant-1")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "This is the last admin of the workspace.")
	assert.Equal(t, http.StatusBadRequest, utils.HTTPStatusCode(err))
}

func TestFindAdmin(t *testing.T) {
	t.Parallel()
	admins := []AdminResponse{
		{ID: "grant-1", Subject: "auth0|alice", Email: "alice@example.com"},
		{ID: "grant-2", Subject: "auth0|bob", Email: "shared@example.com"},
		{ID: "grant-3", Subject: "auth0|carol", Email: "shared@example.com"},
	}
	tests := []struct {
		name    string
		ref     string
		wantID  string
		wantErr string
	}{
		{name: "grant id", ref: "grant-2", wantID: "grant-2"},
		{name: "subject", ref: "auth0|alice", wantID: "grant-1"},
		{name: "email in another case", ref: "Alice@Example.com", wantID: "grant-1"},
		{name: "shared email", ref: "shared@example.com", wantErr: "share the email"},
		{name: "no match", ref: "auth0|dave", wantErr: "no Command ACL admin"},
		{name: "blank", ref: "  ", wantErr: "required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			admin, err := FindAdmin(admins, tt.ref)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantID, admin.ID)
		})
	}
}

func TestToEntryAttributes(t *testing.T) {
	t.Parallel()
	expiry := "2027-01-15T10:00:00Z"
	entries := []EntryResponse{
		{ID: "e1", PrincipalType: "service_token", PrincipalID: testTokenID, CommandPattern: "uptime", Username: "*", ServerIDs: []string{testServerID, "gone"}, Enabled: true},
		{ID: "e2", PrincipalType: "api_token", PrincipalID: "legacy", CommandPattern: "df -h", Username: "root", ExpiresAt: &expiry},
	}

	attributes := ToEntryAttributes(entries, map[string]string{testTokenID: "deploy-bot"}, map[string]string{testServerID: "web-01"})

	require.Len(t, attributes, 2)
	assert.Equal(t, "deploy-bot", attributes[0].Principal)
	assert.Equal(t, "web-01, gone", attributes[0].Servers, "an id with no name is shown as it is")
	assert.Equal(t, "never", attributes[0].ExpiresAt)
	assert.Equal(t, "legacy (api_token)", attributes[1].Principal)
	assert.Equal(t, "all servers", attributes[1].Servers)
	assert.Equal(t, utils.TimeUtils(time.Date(2027, 1, 15, 10, 0, 0, 0, time.UTC)), attributes[1].ExpiresAt)
}

func TestEntryAttributes_JSONFieldNames(t *testing.T) {
	t.Parallel()
	body, err := json.Marshal(EntryAttributes{ID: "e1", Principal: "deploy-bot", Command: "uptime"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"id": "e1", "principal": "deploy-bot", "command_pattern": "uptime", "username": "", "groupname": "", "servers": "", "enabled": false, "expires_at": "", "created_by": ""}`, string(body))
}

// The console never issues an api_token entry nor shows one on a personal API
// token's page, so one issued here could not be seen or revoked there.
func TestIssueEntries_SendsEveryEntryAsAServiceTokens(t *testing.T) {
	t.Parallel()
	ac, stub := newStubSigner(t, func(*http.Request) (int, string) { return http.StatusCreated, `[]` })
	entries := []EntryRequest{
		{PrincipalID: testTokenID, CommandPattern: "uptime", Username: "*"},
		{PrincipalID: testTokenID, CommandPattern: "df -h", Username: "*", ServerIDs: []string{testServerID}},
	}

	_, err := IssueEntries(ac, testWorkspaceID, entries)

	require.NoError(t, err)
	requests := stub.recorded()
	require.Len(t, requests, 1)
	var sent bulkIssueRequest
	require.NoError(t, json.Unmarshal([]byte(requests[0].Body), &sent))
	require.Len(t, sent.Entries, 2)
	for _, entry := range sent.Entries {
		assert.Equal(t, PrincipalTypeServiceToken, entry.PrincipalType)
	}
	assert.Nil(t, entries[0].ServerIDs, "the caller's slice is left as it was")
}

// Listing and revoking keep working for api_token entries already held.
func TestListAndRevoke_KeepAPITokenEntries(t *testing.T) {
	t.Parallel()
	ac, _ := newStubSigner(t, func(r *http.Request) (int, string) {
		if r.Method == http.MethodDelete {
			return http.StatusNoContent, ""
		}
		return http.StatusOK, `{"count": 1, "next": null, "results": [{"id": "` + testEntryID + `", "principal_type": "api_token"}]}`
	})

	entries, err := ListEntries(ac, testWorkspaceID, "")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "api_token", entries[0].PrincipalType)
	require.NoError(t, RevokeEntry(ac, testEntryID))
}

// A workspace listed beside a false flag is not a grant: both halves of the
// answer have to agree.
func TestCapability_AdministersNeedsTheFlag(t *testing.T) {
	t.Parallel()
	capability := Capability{IsLedgerAdmin: false, WorkspaceIDs: []string{testWorkspaceID}}
	assert.False(t, capability.Administers(testWorkspaceID))
}

func TestAdminAttributes_JSONFieldNames(t *testing.T) {
	t.Parallel()
	body, err := json.Marshal(AdminAttributes{ID: "grant-1", Subject: "auth0|alice", Email: "alice@example.com"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"id": "grant-1", "subject": "auth0|alice", "email": "alice@example.com", "granted_by": "", "created_at": ""}`, string(body))
}
