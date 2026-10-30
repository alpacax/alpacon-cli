package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	exitHelperMarker = "exit-helper"
	exitHelperEnv    = "GO_WANT_EXIT_HELPER"
	exitUserUUID     = "11111111-1111-1111-1111-111111111111"

	exitMissingRoleUUID      = "22222222-2222-2222-2222-222222222222"
	exitLargeFileServer      = "big-server"
	exitLargeFileDownloadURL = "/downloads/big"
)

type exitCase struct {
	name       string
	args       []string
	wantExit   int
	wantStderr string
	wantCode   string // set: stderr's last line is an envelope with this error_code; args without --output gain --output json
}

// runExitCases runs each case through the real Execute() in a child process
// with a saved login, so the exit code is the one a script would see.
func runExitCases(t *testing.T, cases []exitCase) {
	t.Helper()
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			args := tt.args
			namesOutput := slices.ContainsFunc(args, func(arg string) bool { return arg == "--output" || strings.HasPrefix(arg, "--output=") })
			if tt.wantCode != "" && !namesOutput {
				args = append([]string{"--output", "json"}, args...)
			}

			stdout, stderr, exitCode := runHelperProcess(t, "TestExitCodeHelperProcess", exitHelperMarker, args,
				exitHelperEnv+"=1", homeEnvVar()+"="+t.TempDir())

			assert.Equal(t, tt.wantExit, exitCode, "stdout: %s\nstderr: %s", stdout, stderr)
			assert.Contains(t, stderr, tt.wantStderr)
			if tt.wantCode != "" {
				lines := strings.Split(strings.TrimSpace(stderr), "\n")
				var envelope struct {
					ErrorCode string `json:"error_code"`
					ExitCode  int    `json:"exit_code"`
				}
				require.NoError(t, json.Unmarshal([]byte(lines[len(lines)-1]), &envelope), "stderr: %s", stderr)
				assert.Equal(t, tt.wantCode, envelope.ErrorCode)
				assert.Equal(t, tt.wantExit, envelope.ExitCode)
			}
		})
	}
}

// exitStubServer answers the lookups commands make before they refuse: the current user, a role
// named admin, and a download of a file over edit's 10 MB limit from big-server; a role named
// flaky answers 503, one named gone and the missing role ID 404, and anything else is an empty list.
func exitStubServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/iam/users/-/":
			_, _ = w.Write([]byte(`{"username":"alice","is_staff":true,"is_superuser":true}`))
		case r.URL.Path == "/api/rbac/roles/" && r.URL.Query().Get("name") == "admin":
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"` + exitUserUUID + `","name":"admin"}]}`))
		case r.URL.Path == "/api/rbac/roles/" && r.URL.Query().Get("name") == "flaky":
			w.WriteHeader(http.StatusServiceUnavailable)
		case r.URL.Path == "/api/rbac/roles/" && r.URL.Query().Get("name") == "gone":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"Not found."}`))
		case r.URL.Path == "/api/rbac/roles/"+exitMissingRoleUUID+"/":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"detail":"Not found."}`))
		case r.URL.Path == "/api/servers/servers/" && r.URL.Query().Get("name") == exitLargeFileServer:
			_, _ = w.Write([]byte(`{"count":1,"results":[{"id":"srv-1","name":"` + exitLargeFileServer + `"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/webftp/downloads/":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"dl-1","command":"cmd-dl-1","download_url":"http://` + r.Host + exitLargeFileDownloadURL + `"}`))
		case r.URL.Path == "/api/events/commands/cmd-dl-1/":
			_, _ = w.Write([]byte(`{"id":"cmd-dl-1","status":"completed","success":true}`))
		case r.URL.Path == exitLargeFileDownloadURL:
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(make([]byte, 10*1024*1024+1))
		case r.URL.Path == "/api/webftp/downloads/dl-1/status/":
			_, _ = w.Write([]byte(`{"success":true}`))
		default:
			_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
		}
	}))
}

// Serial: the child reaches an os.Exit, and the parent's own run returns at once.
func TestExitCodeHelperProcess(t *testing.T) {
	if os.Getenv(exitHelperEnv) != "1" {
		return
	}
	args, ok := helperArgsAfter(os.Args, exitHelperMarker)
	if !ok {
		os.Exit(99)
	}
	ts := exitStubServer()
	defer ts.Close()
	if err := writeExitHelperConfig(ts.URL); err != nil {
		os.Exit(99)
	}
	RootCmd.SetArgs(args)
	Execute()
	os.Exit(0)
}

// writeExitHelperConfig saves a login to the Alpacon Cloud workspace at workspaceURL
// with the kube surface on, so commands behind either gate reach their own checks.
func writeExitHelperConfig(workspaceURL string) error {
	dir := filepath.Join(os.Getenv(homeEnvVar()), ".alpacon")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(map[string]any{
		"workspace_url":           workspaceURL,
		"workspace_name":          "ws",
		"access_token":            "access-token",
		"refresh_token":           "refresh-token",
		"access_token_expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
		"kubernetes_surface":      true,
		"active_work_sessions":    map[string]string{},
	})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600)
}

func TestCommandsExitTwoWhenTheyRejectTheirOwnFlagsOrArguments(t *testing.T) {
	t.Parallel()
	const usage = utils.ExitCodeUsageError
	runExitCases(t, []exitCase{
		{name: "csr create with valid days but no domain or ip", args: []string{"csr", "create", "--valid-days", "5"}, wantExit: usage, wantStderr: "require --domain or --ip"},
		{name: "csr create with valid days of zero", args: []string{"csr", "create", "--domain", "a.com", "--valid-days", "0"}, wantExit: usage, wantStderr: "--valid-days must be a positive integer"},
		{name: "csr create with no usable domain or ip", args: []string{"csr", "create", "--domain", ","}, wantExit: usage, wantStderr: "at least one valid domain or IP"},
		{name: "token delete without yes under json output", args: []string{"token", "delete", "tk"}, wantExit: usage, wantStderr: "This operation requires confirmation", wantCode: utils.ErrorCodeConfirmationRequired},
		{name: "token acl add without a token", args: []string{"token", "acl", "add", "--command", "ls"}, wantExit: usage, wantStderr: "token name or ID is required"},
		{name: "token acl add with a blank command", args: []string{"token", "acl", "add", "tk", "--command", ""}, wantExit: usage, wantStderr: "--command is required"},
		{name: "token acl server add without a server flag", args: []string{"token", "acl", "server", "add", "tk"}, wantExit: usage, wantStderr: "one of --server or --servers is required"},
		{name: "token acl server add with only blank servers", args: []string{"token", "acl", "server", "add", exitUserUUID, "--servers", ","}, wantExit: usage, wantStderr: "--servers must contain at least one server name"},
		{name: "token acl server delete with only blank servers", args: []string{"token", "acl", "server", "delete", exitUserUUID, "--servers", ",", "--yes"}, wantExit: usage, wantStderr: "--servers must contain at least one server name"},
		{name: "token acl file add with an unknown action", args: []string{"token", "acl", "file", "add", "tk", "--path", "/x", "--action", "bad"}, wantExit: usage, wantStderr: "--action must be one of"},
		{name: "token delete without yes", args: []string{"token", "delete", "tk"}, wantExit: usage, wantStderr: "Use --yes"},
		{name: "webhook create with an unknown provider", args: []string{"webhook", "create", "--provider", "bad", "--name", "n", "--url", "http://x"}, wantExit: usage, wantStderr: "Invalid provider"},
		{name: "cp with one argument", args: []string{"cp", "a"}, wantExit: usage, wantStderr: "at least two arguments"},
		{name: "cp with two local paths", args: []string{"cp", "a", "b"}, wantExit: usage, wantStderr: "Invalid combination of source and destination"},
		{name: "cp with mixed users", args: []string{"cp", "a@h:/x", "b@h:/y", "/tmp"}, wantExit: usage, wantStderr: "must use the same user"},
		{name: "cp with mixed local and remote sources", args: []string{"cp", "a", "h:/x", "/tmp"}, wantExit: usage, wantStderr: "cannot mix local and remote"},
		{name: "websh invite without an email", args: []string{"websh", "invite", "s1"}, wantExit: usage, wantStderr: "At least one --email is required"},
		{name: "server create with an unknown platform", args: []string{"server", "create", "--platform", "bad"}, wantExit: usage, wantStderr: "Invalid platform"},
		{name: "server create with an unknown method", args: []string{"server", "create", "--method", "bogus"}, wantExit: usage, wantStderr: "Invalid method"},
		{name: "server create without a platform", args: []string{"server", "create"}, wantExit: usage, wantStderr: "requires --platform"},
		{name: "server create without a token", args: []string{"server", "create", "--platform", "debian"}, wantExit: usage, wantStderr: "requires --token or --new-token"},
		{name: "server token create with a negative expiry", args: []string{"server", "token", "create", "--expires-in-days", "-1"}, wantExit: usage, wantStderr: "--expires-in-days must be 0"},
		{name: "user invite without an email", args: []string{"user", "invite"}, wantExit: usage, wantStderr: "email argument is required"},
		{name: "user invite with an invalid email", args: []string{"user", "invite", "not-an-email"}, wantExit: usage, wantStderr: "invalid email address"},
		{name: "user role grant with the arguments transposed", args: []string{"user", "role", "grant", "admin", "bob"}, wantExit: usage, wantStderr: "The user comes first"},
		{name: "user role grant with a role ID given first", args: []string{"user", "role", "grant", "admin", exitMissingRoleUUID}, wantExit: usage, wantStderr: "The user comes first"},
		{name: "user role revoke cascade on a plain role", args: []string{"user", "role", "revoke", exitUserUUID, "admin", "--cascade"}, wantExit: usage, wantStderr: "--cascade only applies"},
		{name: "user permission can-i with the arguments transposed", args: []string{"user", "permission", "can-i", "server:update", "bob"}, wantExit: usage, wantStderr: "The user comes first"},
		{name: "user permission can-i with a non permission", args: []string{"user", "permission", "can-i", "foo"}, wantExit: usage, wantStderr: "is not a permission"},
		{name: "exec purpose with a job id that is not a uuid", args: []string{"exec", "purpose", "x", "because"}, wantExit: usage, wantStderr: "invalid JOB_ID"},
		{name: "exec purpose with a blank purpose", args: []string{"exec", "purpose", exitUserUUID, "  "}, wantExit: usage, wantStderr: "PURPOSE requires a value describing what this command is for"},
		{name: "exec logs with a job id that is not a uuid", args: []string{"exec", "logs", "x"}, wantExit: usage, wantStderr: "invalid JOB_ID"},
		{name: "note create with content over the limit", args: []string{"note", "create", "-s", "my-server", "-c", strings.Repeat("a", 513)}, wantExit: usage, wantStderr: "512 character limit"},
		{name: "login without a target", args: []string{"login"}, wantExit: usage, wantStderr: "login target is required"},
		{name: "login with a workspace but no region", args: []string{"login", "--workspace", "x"}, wantExit: usage, wantStderr: "--region is required"},
		{name: "kube config with a bad proxy port", args: []string{"kube", "config", "c", "--proxy-port", "0"}, wantExit: usage, wantStderr: "--proxy-port must be a port number from 1 to 65535, not 0."},
		{name: "kube proxy with a bad port", args: []string{"kube", "proxy", "c", "--port", "99999"}, wantExit: usage, wantStderr: "--port must be a port number from 1 to 65535, not 99999."},
		{name: "approval ls with an unknown status", args: []string{"approval", "ls", "--status", "bad"}, wantExit: usage, wantStderr: "invalid --status"},
		{name: "approval ls with an unknown type", args: []string{"approval", "ls", "--type", "bad"}, wantExit: usage, wantStderr: "invalid --type"},
		{name: "acl entry add with a file and a flag", args: []string{"acl", "entry", "add", "--file", "x", "--command", "ls"}, wantExit: usage, wantStderr: "--file cannot be combined"},
		{name: "acl entry add without a token", args: []string{"acl", "entry", "add"}, wantExit: usage, wantStderr: "--token is required"},
		{name: "acl entry add with a bad expiry", args: []string{"acl", "entry", "add", "--token", "tk", "--command", "ls", "--username", "root", "--expires-at", "bad"}, wantExit: usage, wantStderr: `--expires-at must be an RFC 3339 time such as 2027-01-15T10:00:00Z, not "bad"`},
		{name: "acl entry add with only a blank server", args: []string{"acl", "entry", "add", "--token", "tk", "--command", "ls", "--username", "root", "--server", ","}, wantExit: usage, wantStderr: "a server value is empty; name each server, or leave the scope out on purpose to cover every server"},
		{name: "acl entry remove with a name", args: []string{"acl", "entry", "remove", "foo"}, wantExit: usage, wantStderr: "is not an entry ID"},
		{name: "edit with a local path", args: []string{"edit", "foo"}, wantExit: usage, wantStderr: "remote path must be in format"},
		{name: "edit of a large file without force", args: []string{"edit", exitLargeFileServer + ":/var/log/big.log"}, wantExit: usage, wantStderr: "rerun with --force"},
		{name: "edit of a large file without force under json output", args: []string{"edit", exitLargeFileServer + ":/var/log/big.log"}, wantExit: usage, wantStderr: "rerun with --force", wantCode: utils.ErrorCodeUsage},
		{name: "tunnel with an invalid remote port", args: []string{"tunnel", "my-server", "-l", "9000", "-r", "invalid"}, wantExit: usage, wantStderr: "invalid remote port"},
		{name: "api without an endpoint under json output", args: []string{"api"}, wantExit: usage, wantStderr: "expected one ENDPOINT", wantCode: utils.ErrorCodeUsage},
		{name: "api with an invalid method under json output", args: []string{"api", "-X", "NOT A METHOD", "/api/x/"}, wantExit: usage, wantStderr: "invalid method", wantCode: utils.ErrorCodeUsage},
	})
}

func TestCommandsExitOneWhenAValidCommandLineFails(t *testing.T) {
	t.Parallel()
	runExitCases(t, []exitCase{
		{name: "user role grant whose role lookup fails", args: []string{"user", "role", "grant", "admin", "flaky"}, wantExit: utils.ExitCodeGeneralError, wantStderr: "Failed to resolve the role"},
		{name: "user role grant whose role lookup by name answers 404", args: []string{"user", "role", "grant", "admin", "gone"}, wantExit: utils.ExitCodeGeneralError, wantStderr: "Failed to resolve the role"},
		{name: "user role grant of a role the server does not have", args: []string{"user", "role", "grant", "alice", "ghost"}, wantExit: utils.ExitCodeGeneralError, wantStderr: `no role named "ghost" is visible to you`},
	})
}
