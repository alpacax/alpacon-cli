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
	for _, tt := range []struct {
		name, username, groupname    string
		file                         bool
		nullApplication, lookupFails bool
		wantExit                     int
	}{
		{name: "default"}, {name: "confirmed", username: "deploy", groupname: "deploy"},
		{name: "other username", username: "root", wantExit: 2}, {name: "wildcard username", username: "*", wantExit: 2},
		{name: "other group", groupname: "root", wantExit: 2}, {name: "wildcard group", groupname: "*", wantExit: 2},
		{name: "batch default", file: true}, {name: "batch other username", username: "root", file: true, wantExit: 2},
		{name: "batch other group", groupname: "root", file: true, wantExit: 2},
		{name: "null application", nullApplication: true, wantExit: 1},
		{name: "workspace lookup fails", lookupFails: true, wantExit: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			workspace := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			signer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				signerRequests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/acl/me/" {
					_, _ = fmt.Fprint(w, adminOfWorkspace)
					return
				}
				assert.Equal(t, "/api/acl/entries/bulk/", r.URL.Path)
				var body struct {
					Entries []acl.EntryRequest `json:"entries"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); !assert.NoError(t, err) {
					w.WriteHeader(500)
					return
				}
				if assert.Len(t, body.Entries, 1) {
					assert.Equal(t, "deploy", body.Entries[0].Username)
					assert.Equal(t, fileTokenID, body.Entries[0].PrincipalID)
				}
				_, _ = fmt.Fprint(w, `[]`)
			}))
			defer signer.Close()
			flags := []string{"--token", "deploy-bot", "--command", "uptime"}
			if tt.file {
				file := t.TempDir() + "/entries.json"
				require.NoError(t, os.WriteFile(file, fileOf(t, fileItem(map[string]any{"username": tt.username, "groupname": tt.groupname})), 0600))
				flags = []string{"--file", file}
			} else {
				if tt.username != "" {
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
				field := "username"
				if tt.groupname != "" {
					field = "groupname"
				}
				assert.Equal(t, fmt.Sprintf("entry 0: %s: the account must be the token's application account %q (code: service_token_account_not_own)", field, "deploy"), envelope.Message)
			} else {
				assert.True(t, strings.HasSuffix(envelope.Message, "."), "session error must end in a period: %q", envelope.Message)
				assert.Contains(t, envelope.Message, "Entry 0:")
				if tt.nullApplication {
					assert.Contains(t, envelope.Message, "did not return the service token's application username")
				} else {
					assert.Equal(t, "auth_permission_denied", envelope.ErrorCode)
					assert.Contains(t, envelope.Message, "failed to find service token")
				}
			}
			assert.Equal(t, int32(0), signerRequests.Load())
		})
	}
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
		{"own group", acl.PrincipalTypeServiceToken, "deploy", "deploy", true, true},
		{"other username", acl.PrincipalTypeServiceToken, "root", "", true, false},
		{"other group", acl.PrincipalTypeServiceToken, "deploy", "root", true, false},
		{"wildcard", acl.PrincipalTypeServiceToken, "*", "", true, false},
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
			if !tt.known || tt.principal != "service_token" {
				assert.Nil(t, rows[0].AppliesToToken)
				assert.Empty(t, rows[0].Applies)
				return
			}
			require.NotNil(t, rows[0].AppliesToToken)
			assert.Equal(t, tt.applies, *rows[0].AppliesToToken)
			if tt.applies {
				assert.Empty(t, rows[0].Applies)
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
