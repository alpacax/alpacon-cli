package acl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/api/acl"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/httpclient"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEntryAdd_ApplicationAccount(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, username, groupname    string
		file                         bool
		secondEntry                  bool
		usernameGiven                bool
		nullApplication, lookupFails bool
		wantExit                     int
	}{
		{name: "default"}, {name: "confirmed", username: "deploy", groupname: "deploy"},
		{name: "explicit empty username", usernameGiven: true},
		{name: "other username", username: "root", wantExit: 2}, {name: "wildcard username", username: "*"},
		{name: "other group", groupname: "root", wantExit: 2}, {name: "wildcard group", groupname: "*"},
		{name: "wildcard accounts", username: "*", groupname: "*"},
		{name: "batch default", file: true},
		{name: "batch explicit empty username", usernameGiven: true, file: true},
		{name: "batch wildcard username", username: "*", file: true},
		{name: "batch confirmed", username: "deploy", groupname: "deploy", file: true},
		{name: "batch other username", username: "root", file: true, wantExit: 2},
		{name: "batch second username", username: "root", file: true, secondEntry: true, wantExit: 2},
		{name: "batch other group", groupname: "root", file: true, wantExit: 2},
		{name: "batch wildcard accounts", username: "*", groupname: "*", file: true},
		{name: "null application", nullApplication: true, wantExit: 1},
		{name: "workspace lookup fails", lookupFails: true, wantExit: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var tokenLookups atomic.Int32
			workspace := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tokenLookups.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if tt.lookupFails {
					w.WriteHeader(http.StatusForbidden)
					_, _ = fmt.Fprint(w, `{"code":"auth_permission_denied","detail":"Not allowed."}`)
					return
				}
				token := `{"id":"` + fileTokenID + `","name":"deploy-bot","application":{"username":"deploy"}}`
				if tt.nullApplication {
					token = `{"id":"` + fileTokenID + `","name":"deploy-bot","application":null}`
				}
				if r.URL.Path == "/api/auth/service-tokens/tokens/" {
					_, _ = fmt.Fprintf(w, `{"count":1,"next":null,"results":[%s]}`, token)
				} else {
					assert.Equal(t, "/api/auth/service-tokens/tokens/"+fileTokenID+"/", r.URL.Path)
					_, _ = fmt.Fprint(w, token)
				}
			}))
			defer workspace.Close()
			var signerRequests atomic.Int32
			var signerWrites atomic.Int32
			signer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				signerRequests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/acl/me/" {
					_, _ = fmt.Fprint(w, adminOfWorkspace)
					return
				}
				assert.Equal(t, "/api/acl/entries/bulk/", r.URL.Path)
				signerWrites.Add(1)
				var body struct {
					Entries []acl.EntryRequest `json:"entries"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); !assert.NoError(t, err) {
					w.WriteHeader(500)
					return
				}
				if assert.Len(t, body.Entries, 1) {
					username := tt.username
					if username == "" && !tt.usernameGiven {
						username = "deploy"
					}
					assert.Equal(t, username, body.Entries[0].Username)
					assert.Equal(t, tt.groupname, body.Entries[0].Groupname)
					assert.Equal(t, fileTokenID, body.Entries[0].PrincipalID)
				}
				_, _ = fmt.Fprint(w, `[]`)
			}))
			defer signer.Close()
			flags := []string{"--token", "deploy-bot", "--command", "uptime"}
			if tt.file {
				file := t.TempDir() + "/entries.json"
				var username any = tt.username
				if tt.username == "" && !tt.usernameGiven {
					username = nil
				}
				items := []map[string]any{fileItem(map[string]any{"username": username, "groupname": tt.groupname})}
				if tt.secondEntry {
					items = append([]map[string]any{fileItem(nil)}, items...)
				}
				require.NoError(t, os.WriteFile(file, fileOf(t, items...), 0600))
				flags = []string{"--file", file}
			} else {
				if tt.username != "" || tt.usernameGiven {
					flags = append(flags, "--username", tt.username)
				}
				if tt.groupname != "" {
					flags = append(flags, "--groupname", tt.groupname)
				}
			}
			data, err := json.Marshal(flags)
			require.NoError(t, err)
			child := exec.Command(os.Args[0], "-test.run=^TestEntryAddApplicationAccountHelper$")
			child.Env = append(os.Environ(), "ALPACON_ACCOUNT_HELPER="+string(data), "ALPACON_ACCOUNT_WORKSPACE="+workspace.URL, client.SignerURLEnvVar+"="+signer.URL)
			var stderr bytes.Buffer
			child.Stderr = &stderr
			err = child.Run()
			if tt.wantExit == 0 {
				require.NoError(t, err, stderr.String())
				assert.Equal(t, int32(2), signerRequests.Load())
				assert.Equal(t, int32(1), signerWrites.Load())
				return
			}
			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, tt.wantExit, exitErr.ExitCode())
			var envelope utils.JSONErrorEnvelope[map[string]any]
			require.NoError(t, json.Unmarshal(stderr.Bytes(), &envelope))
			assert.Equal(t, tt.wantExit, envelope.ExitCode)
			if tt.wantExit == 2 {
				assert.Equal(t, utils.ServiceTokenAccountNotOwn, envelope.ErrorCode)
				field, noun := "Username", "account"
				if tt.groupname != "" {
					field, noun = "Groupname", "group"
				}
				prefix := ""
				if tt.file {
					prefix, field = "Entry 0: ", strings.ToLower(field)
					if tt.secondEntry {
						prefix = "Entry 1: "
						assert.Equal(t, int32(1), tokenLookups.Load())
					}
				}
				assert.Equal(t, fmt.Sprintf("%s%s: the %s must be empty, * or %q (code: service_token_account_not_own).", prefix, field, noun, "deploy"), envelope.Message)
			} else {
				assert.True(t, strings.HasSuffix(envelope.Message, "."), "session error must end in a period: %q", envelope.Message)
				assert.NotContains(t, envelope.Message, "Entry 0:")
				if tt.nullApplication {
					assert.Contains(t, envelope.Message, "did not return the service token's application username")
				} else {
					assert.Equal(t, "auth_permission_denied", envelope.ErrorCode)
					assert.Contains(t, envelope.Message, "Failed to find service token")
				}
			}
			assert.Equal(t, int32(0), signerRequests.Load())
		})
	}
}

func TestBindApplicationAccounts_ReturnsAccountRefusal(t *testing.T) {
	t.Parallel()
	workspace := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":%q,"application":{"username":"deploy"}}`, fileTokenID)
	}))
	t.Cleanup(workspace.Close)
	session := &ledgerSession{workspace: &client.AlpaconClient{HTTPClient: workspace.Client(), BaseURL: workspace.URL}, workspaceURL: workspace.URL}
	for _, field := range []string{"username", "groupname"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			entry := entryDraft{EntryRequest: acl.EntryRequest{PrincipalID: fileTokenID}}
			if field == "username" {
				entry.Username = "root"
			} else {
				entry.Groupname = "root"
			}
			err := session.bindApplicationAccounts([]entryDraft{entry}, false)
			require.Error(t, err)
			assert.Equal(t, utils.ServiceTokenAccountNotOwn, utils.StructuredErrorCode(err))
			assert.Contains(t, err.Error(), field+":")
		})
	}
}

func TestEntryAdd_AccountHelp(t *testing.T) {
	assert.Contains(t, strings.Join(strings.Fields(entryAddCmd.Long), " "), "any account bound to the application on that server")
	assert.Contains(t, entryAddCmd.Flags().Lookup("username").Usage, "Omit to pin the entry to the application's own account")
	assert.Contains(t, entryAddCmd.Flags().Lookup("groupname").Usage, "'' or * sets no group restriction")
	assert.NotContains(t, entryAddCmd.Long, "same account")
	assert.Contains(t, strings.Join(strings.Fields(entryListCmd.Long), " "), "blank when the application account cannot be resolved")
}

func TestEntryAddApplicationAccountHelper(t *testing.T) {
	data := os.Getenv("ALPACON_ACCOUNT_HELPER")
	if data == "" {
		return
	}
	seams := &sessionSeams{cfg: loggedIn("cached"), env: ledgerEnv()}
	seams.cfg.WorkspaceURL = os.Getenv("ALPACON_ACCOUNT_WORKSPACE")
	installSeams(t, seams)
	newWorkspaceClient = func() (*client.AlpaconClient, error) {
		return &client.AlpaconClient{HTTPClient: httpclient.New(false), BaseURL: seams.cfg.WorkspaceURL}, nil
	}
	var flags []string
	require.NoError(t, json.Unmarshal([]byte(data), &flags))
	require.NoError(t, entryAddCmd.Flags().Parse(flags))
	utils.OutputFormat = utils.OutputFormatJSON
	runEntryAdd(entryAddCmd, nil)
}

func TestLedgerEntryAttributes_ApplicationAccount(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, principal, username, groupname string
		known                                bool
		applies                              bool
	}{
		{"own", acl.PrincipalTypeServiceToken, "deploy", "", true, true},
		{"empty username", acl.PrincipalTypeServiceToken, "", "", true, true},
		{"empty username with own group", acl.PrincipalTypeServiceToken, "", "deploy", true, true},
		{"empty username with wildcard group", acl.PrincipalTypeServiceToken, "", "*", true, true},
		{"empty username with other group", acl.PrincipalTypeServiceToken, "", "root", true, false},
		{"own group", acl.PrincipalTypeServiceToken, "deploy", "deploy", true, true},
		{"other username", acl.PrincipalTypeServiceToken, "root", "", true, false},
		{"other group", acl.PrincipalTypeServiceToken, "deploy", "root", true, false},
		{"wildcard username", acl.PrincipalTypeServiceToken, "*", "", true, true},
		{"wildcard group", acl.PrincipalTypeServiceToken, "deploy", "*", true, true},
		{"wildcard accounts", acl.PrincipalTypeServiceToken, "*", "*", true, true},
		{"wildcard username with own group", acl.PrincipalTypeServiceToken, "*", "deploy", true, true},
		{"wildcard username with other group", acl.PrincipalTypeServiceToken, "*", "root", true, false},
		{"other username with wildcard group", acl.PrincipalTypeServiceToken, "root", "*", true, false},
		{"unknown account", acl.PrincipalTypeServiceToken, "deploy", "", false, false},
		{"legacy principal", "api_token", "root", "", true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			accounts := map[string]string{}
			if tt.known {
				accounts[fileTokenID] = "deploy"
			}
			rows := ledgerEntryAttributes([]acl.EntryResponse{{PrincipalID: fileTokenID, PrincipalType: tt.principal, Username: tt.username, Groupname: tt.groupname}}, nil, nil, accounts)
			require.Len(t, rows, 1)
			assert.Equal(t, tt.username, rows[0].Username)
			assert.Equal(t, tt.groupname, rows[0].Groupname)
			if !tt.known || tt.principal != "service_token" {
				assert.Nil(t, rows[0].AppliesToToken)
				assert.Empty(t, rows[0].Applies)
				return
			}
			require.NotNil(t, rows[0].AppliesToToken)
			assert.Equal(t, tt.applies, *rows[0].AppliesToToken)
			if tt.applies {
				assert.Equal(t, "yes", rows[0].Applies)
			} else {
				assert.Equal(t, "no", rows[0].Applies)
			}
			raw, err := json.Marshal(rows)
			require.NoError(t, err)
			assert.Contains(t, string(raw), fmt.Sprintf(`"applies_to_token":%t`, tt.applies))
		})
	}
}

func TestLedgerList_ReadsApplicationAccountFromWorkspace(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/auth/service-tokens/tokens/", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"count":1,"next":null,"results":[{"id":%q,"name":"deploy-bot","application":{"username":"deploy"}}]}`, fileTokenID)
	}))
	defer ts.Close()
	session := &ledgerSession{workspace: &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL}, workspaceURL: ts.URL}
	entries := []acl.EntryResponse{{PrincipalType: acl.PrincipalTypeServiceToken, PrincipalID: fileTokenID, Username: "root"}}
	tokens, servers, accounts := session.workspaceNames(entries)
	rows := ledgerEntryAttributes(entries, tokens, servers, accounts)
	require.Len(t, rows, 1)
	assert.Equal(t, "deploy-bot", rows[0].Principal)
	assert.Equal(t, "no", rows[0].Applies)
	require.NotNil(t, rows[0].AppliesToToken)
	assert.False(t, *rows[0].AppliesToToken, "root must not match the application's deploy account")
}
