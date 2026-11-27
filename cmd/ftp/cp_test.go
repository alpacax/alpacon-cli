package ftp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const storageNotFoundBody = `<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchKey</Code><Message>The specified key does not exist.</Message></Error>`

func TestReportDownloadRejectionExitsSixInEitherFormat(t *testing.T) {
	t.Parallel()
	for _, format := range []string{utils.OutputFormatTable, utils.OutputFormatJSON} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			ios, _, stderr := utils.TestIOStreams()
			require.NoError(t, ios.SetOutputFormat(format))
			rejected := &event.CommandRejectedError{Cause: "account_bound_to_other_person", Reported: true, ConsoleURL: "https://console.example/commands/1", WorkSessionID: "550e8400-e29b-41d4-a716-446655440000"}

			err := reportDownloadRejection(fmt.Errorf("download command: %w", rejected), "[prod:/a]")

			var exitErr *utils.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, utils.ExitCodeNotApproved, exitErr.Code)
			ios.FprintExitError(stderr, exitErr)
			if format == utils.OutputFormatTable {
				assert.Contains(t, stderr.String(), "Failed to download from '[prod:/a]': command was rejected")
				return
			}
			var envelope utils.JSONErrorEnvelope[map[string]any]
			require.NoError(t, json.Unmarshal(stderr.Bytes(), &envelope), "stderr: %s", stderr.String())
			assert.Equal(t, utils.ExitCodeNotApproved, envelope.ExitCode)
			assert.Equal(t, "account_bound_to_other_person", envelope.ErrorCode)
			assert.Equal(t, "Failed to download from '[prod:/a]': command was rejected: the account it would run as was linked to another person before it was delivered (rejection_cause: account_bound_to_other_person)", envelope.Message)
			assert.Equal(t, "download", envelope.Context["operation"])
			assert.Equal(t, map[string]any{"console_url": "https://console.example/commands/1", "work_session_id": "550e8400-e29b-41d4-a716-446655440000", "rejection_cause": "account_bound_to_other_person", "rejection_gate": nil}, envelope.Server)
		})
	}
}

func TestIsRemotePath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{
			name:     "local file path",
			path:     "./test.txt",
			expected: false,
		},
		{
			name:     "local absolute path",
			path:     "/home/user/test.txt",
			expected: false,
		},
		{
			name:     "simple hostname only",
			path:     "prod-docker",
			expected: false,
		},
		{
			name:     "user and hostname only",
			path:     "root@prod-docker",
			expected: false,
		},
		{
			name:     "remote path with hostname",
			path:     "prod-docker:~/",
			expected: true,
		},
		{
			name:     "remote path with user and hostname",
			path:     "root@prod-docker:/var/log/syslog",
			expected: true,
		},
		{
			name:     "complex remote path",
			path:     "admin@prod-docker:~/alice/test.txt",
			expected: true,
		},
		{
			name:     "empty remote path",
			path:     "server:",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRemotePath(tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestValidatePaths(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		sources    []string
		dest       string
		wantErrSub string
	}{
		{
			name:    "local sources with remote destination",
			sources: []string{"./test.txt", "/home/user/file.txt"},
			dest:    "myserver:/tmp/",
		},
		{
			name:    "remote source with local destination",
			sources: []string{"myserver:/tmp/file.txt"},
			dest:    "./",
		},
		{
			// planCopy strips the user prefix before validate runs; this pins
			// that SplitPath itself does not, so the server name keeps "admin@".
			name:    "remote source with a user prefix",
			sources: []string{"admin@myserver:/tmp/file.txt"},
			dest:    "./",
		},
		{
			name:    "Windows drive-letter source with remote destination",
			sources: []string{`C:\Users\alice\notes.txt`},
			dest:    "myserver:/tmp/",
		},
		{
			name:       "Windows drive-letter source mixed with a remote source",
			sources:    []string{`C:\Users\alice\notes.txt`, "myserver:/tmp/file.txt"},
			dest:       "/tmp/",
			wantErrSub: "cannot mix local and remote source paths",
		},
		{
			name:       "mixed local and remote sources",
			sources:    []string{"./test.txt", "myserver:/tmp/file.txt"},
			dest:       "/tmp/",
			wantErrSub: "cannot mix local and remote source paths",
		},
		{
			name:       "remote source without server name",
			sources:    []string{":/tmp/file.txt"},
			dest:       "./",
			wantErrSub: "invalid remote path format",
		},
		{
			name:       "remote destination without path",
			sources:    []string{"./test.txt"},
			dest:       "myserver:",
			wantErrSub: "invalid remote path format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append(slices.Clone(tt.sources), tt.dest)
			err := copyPlan{sources: tt.sources, dest: tt.dest, remote: classifyPaths(args)}.validate()
			if tt.wantErrSub == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.wantErrSub)
		})
	}
}

func TestCpCommandSSHParsing(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		args         []string
		expectedArgs []string
		expectedUser string
	}{
		{
			name:         "simple local to remote copy",
			args:         []string{"test.txt", "prod-docker:~/"},
			expectedArgs: []string{"test.txt", "prod-docker:~/"},
			expectedUser: "",
		},
		{
			name:         "local to remote with path",
			args:         []string{"test.txt", "prod-docker:~/alice/"},
			expectedArgs: []string{"test.txt", "prod-docker:~/alice/"},
			expectedUser: "",
		},
		{
			name:         "remote to local copy",
			args:         []string{"prod-docker:~/alice/test.txt", "."},
			expectedArgs: []string{"prod-docker:~/alice/test.txt", "."},
			expectedUser: "",
		},
		{
			name:         "remote with user to local",
			args:         []string{"root@prod-docker:/var/log/syslog", "."},
			expectedArgs: []string{"prod-docker:/var/log/syslog", "."},
			expectedUser: "root",
		},
		{
			name:         "local to remote with user",
			args:         []string{"test.txt", "admin@prod-docker:~/uploads/"},
			expectedArgs: []string{"test.txt", "prod-docker:~/uploads/"},
			expectedUser: "admin",
		},
		{
			name:         "multiple local files to remote with user",
			args:         []string{"file1.txt", "file2.txt", "deploy@web-server:/opt/app/"},
			expectedArgs: []string{"file1.txt", "file2.txt", "web-server:/opt/app/"},
			expectedUser: "deploy",
		},
		{
			name:         "user in hostname only without colon — not parsed as SSH",
			args:         []string{"test.txt", "root@prod-docker"},
			expectedArgs: []string{"test.txt", "root@prod-docker"},
			expectedUser: "",
		},
		{
			name:         "local file with @ in name — not parsed as SSH",
			args:         []string{"report@2026.txt", "prod-docker:~/uploads/"},
			expectedArgs: []string{"report@2026.txt", "prod-docker:~/uploads/"},
			expectedUser: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := make([]string, len(tt.args))
			copy(args, tt.args)

			username, err := normalizeArgs(args, "")

			require.NoError(t, err)
			assert.Equal(t, tt.expectedArgs, args)
			assert.Equal(t, tt.expectedUser, username)
		})
	}
}

func TestCopyPlanDirection(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		args         []string
		wantUpload   bool
		wantDownload bool
	}{
		{name: "local file to remote directory", args: []string{"test.txt", "prod-docker:~/"}, wantUpload: true},
		{name: "several local files to remote directory", args: []string{"a.txt", "b.txt", "prod-docker:~/alice/"}, wantUpload: true},
		{name: "remote file to current directory", args: []string{"prod-docker:~/alice/test.txt", "."}, wantDownload: true},
		{name: "remote file with a user to current directory", args: []string{"root@prod-docker:/var/log/syslog", "."}, wantDownload: true},
		{name: "drive-letter file to remote directory", args: []string{`C:\Users\alice\notes.txt`, "my-server:/tmp/"}, wantUpload: true},
		{name: "remote file to drive-letter directory", args: []string{"my-server:/tmp/notes.txt", `C:\dl\`}, wantDownload: true},
		{name: "local file with a colon to remote directory", args: []string{"./a:b.txt", "my-server:/tmp/"}, wantUpload: true},
		{name: "user-prefixed single-letter server keeps its remote reading", args: []string{`alice@h:\notes.txt`, "./out"}, wantDownload: true},
		{name: "local file to user-prefixed single-letter server", args: []string{"notes.txt", `alice@h:\tmp\`}, wantUpload: true},
		{name: "two local paths are neither", args: []string{"a", "b"}},
		{name: "two remote paths are neither", args: []string{"s1:/a", "s2:/b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			args := slices.Clone(tt.args)

			plan, err := planCopy(args, "")

			require.NoError(t, err)
			assert.Equal(t, tt.wantUpload, plan.isUpload())
			assert.Equal(t, tt.wantDownload, plan.isDownload())
		})
	}
}

func TestPlanCopyClassifiesBeforeTheUserPrefixIsStripped(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		args        []string
		wantRemote  []bool
		wantSources []string
		wantDest    string
		wantUser    string
	}{
		{
			name:        "remote source with a user and a backslash path",
			args:        []string{`alice@h:\notes.txt`, "./out"},
			wantRemote:  []bool{true, false},
			wantSources: []string{`h:\notes.txt`},
			wantDest:    "./out",
			wantUser:    "alice",
		},
		{
			name:        "remote destination with a user and a backslash path",
			args:        []string{"notes.txt", `alice@h:\tmp\`},
			wantRemote:  []bool{false, true},
			wantSources: []string{"notes.txt"},
			wantDest:    `h:\tmp\`,
			wantUser:    "alice",
		},
		{
			name:        "drive-letter source stays untouched",
			args:        []string{`C:\Users\alice\notes.txt`, "my-server:/tmp/"},
			wantRemote:  []bool{false, true},
			wantSources: []string{`C:\Users\alice\notes.txt`},
			wantDest:    "my-server:/tmp/",
		},
		{
			name:        "local name holding an at sign and a colon is not split",
			args:        []string{"./a@b:c.txt", "my-server:/tmp/"},
			wantRemote:  []bool{false, true},
			wantSources: []string{"./a@b:c.txt"},
			wantDest:    "my-server:/tmp/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			args := slices.Clone(tt.args)

			plan, err := planCopy(args, "")

			require.NoError(t, err)
			assert.Equal(t, tt.wantRemote, plan.remote)
			assert.Equal(t, tt.wantSources, plan.sources)
			assert.Equal(t, tt.wantDest, plan.dest)
			assert.Equal(t, tt.wantUser, plan.username)
		})
	}
}

func TestStripUserPrefix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		arg           string
		wantRewritten string
		wantUser      string
	}{
		{name: "user, host and path", arg: "alice@myserver:/tmp/f", wantRewritten: "myserver:/tmp/f", wantUser: "alice"},
		{name: "dot-slash local name holding an at sign and a colon", arg: "./a@b:c.txt", wantRewritten: "./a@b:c.txt"},
		{name: "user and a single-letter host with a backslash path", arg: `alice@h:\notes.txt`, wantRewritten: `h:\notes.txt`, wantUser: "alice"},
		{name: "user and host with a trailing colon", arg: "alice@myserver:", wantRewritten: "myserver:", wantUser: "alice"},
		{name: "host with a trailing colon is untouched", arg: "myserver:", wantRewritten: "myserver:"},
		{name: "user and an empty host", arg: "alice@:", wantRewritten: ":", wantUser: "alice"},
		{name: "empty user before the host is dropped", arg: "@myserver:/tmp/f", wantRewritten: "myserver:/tmp/f"},
		{name: "at sign in the remote path is untouched", arg: "myserver:/tmp/a@b", wantRewritten: "myserver:/tmp/a@b"},
		{name: "no user is untouched", arg: "myserver:/tmp/f", wantRewritten: "myserver:/tmp/f"},
		{name: "local path with an @ but no colon is untouched", arg: "./mail@example.txt", wantRewritten: "./mail@example.txt"},
		{name: "path may itself contain an @", arg: "alice@myserver:/tmp/mail@example.txt", wantRewritten: "myserver:/tmp/mail@example.txt", wantUser: "alice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rewritten, user := stripUserPrefix(tt.arg)
			assert.Equal(t, tt.wantRewritten, rewritten)
			assert.Equal(t, tt.wantUser, user)
		})
	}
}

func TestNormalizeArgsUsername(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		args         []string
		flagUser     string
		wantUser     string
		wantArgs     []string
		wantErrUsers []string
	}{
		{
			name:     "the -u flag wins over an inline user",
			args:     []string{"test.txt", "alice@myserver:/tmp/"},
			flagUser: "carol",
			wantUser: "carol",
			wantArgs: []string{"test.txt", "myserver:/tmp/"},
		},
		{
			name:     "the -u flag suppresses an inline user conflict",
			args:     []string{"alice@myserver:/tmp/f", "bob@myserver:/tmp/g"},
			flagUser: "carol",
			wantUser: "carol",
			wantArgs: []string{"myserver:/tmp/f", "myserver:/tmp/g"},
		},
		{
			name:     "agreeing inline users resolve to that user",
			args:     []string{"alice@myserver:/tmp/f", "alice@other:/tmp/g"},
			wantUser: "alice",
			wantArgs: []string{"myserver:/tmp/f", "other:/tmp/g"},
		},
		{
			name:         "conflicting inline users are rejected",
			args:         []string{"alice@myserver:/tmp/f", "bob@myserver:/tmp/g"},
			wantArgs:     []string{"alice@myserver:/tmp/f", "bob@myserver:/tmp/g"},
			wantErrUsers: []string{"alice", "bob"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user, err := normalizeArgs(tt.args, tt.flagUser)
			if len(tt.wantErrUsers) > 0 {
				for _, u := range tt.wantErrUsers {
					require.ErrorContains(t, err, u)
				}
				assert.Equal(t, tt.wantArgs, tt.args)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantUser, user)
			assert.Equal(t, tt.wantArgs, tt.args)
		})
	}
}

// fileRefusal returns the error the client makes of a 403 answered with body.
func fileRefusal(t *testing.T, body string) error {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	ac := &client.AlpaconClient{HTTPClient: ts.Client(), BaseURL: ts.URL, Token: "test-token"}
	_, err := ac.SendGetRequest("/api/webftp/uploads/")
	require.Error(t, err)
	return err
}

// The run-as-root advice fits a permission on the server's files; a refusal carrying the
// server's hint, such as a token's file access rules, gets the hint instead.
func TestWantsPermissionAdvice(t *testing.T) {
	t.Parallel()
	const hint = "This token's file access rules do not allow this transfer for this path, account and direction."
	fileACL := `{"code": "api_token_acl_not_allowed", "gate": "file_acl", "missing": "file_preapproval"`

	hinted := fileRefusal(t, fileACL+`, "hint": "`+hint+`"}`)
	require.Contains(t, hinted.Error(), "permission denied")
	assert.False(t, wantsPermissionAdvice(hinted))
	assert.Equal(t, hint, utils.RefusalHint(hinted))
	// A bulk upload joins its files' failures; a refusal among them still
	// reads through.
	assert.False(t, wantsPermissionAdvice(errors.Join(errors.New("a.txt: disk full"), fmt.Errorf("b.txt: %w", hinted))))

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "a file permission on the server", err: errors.New("open /etc/shadow: permission denied"), want: true},
		{name: "an access denied from the agent", err: errors.New("access denied"), want: true},
		{name: "a refusal from a server that sends no hint", err: fileRefusal(t, `{"code": "user_no_permission"}`), want: true},
		{name: "an unrelated failure", err: errors.New("connection reset by peer")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, wantsPermissionAdvice(tt.err), "err: %v", tt.err)
		})
	}
}

// refusedDownloadClient serves a download whose final fetch is answered with
// status and body.
func refusedDownloadClient(status int, body string) *client.AlpaconClient {
	return &client.AlpaconClient{
		BaseURL:       testutil.StubBaseURL,
		WorkspaceName: "my-workspace",
		HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
			switch {
			case r.URL.Path == "/api/servers/servers/":
				return http.StatusOK, `{"count": 1, "results": [{"id": "srv-id", "name": "my-server"}]}`
			case r.Method == http.MethodPost && r.URL.Path == "/api/webftp/downloads/":
				return http.StatusCreated, `{"id": "t-1", "command": "cmd-1", "download_url": "http://stub.invalid/blob"}`
			case r.URL.Path == "/api/events/commands/cmd-1/":
				return http.StatusOK, `{"id": "cmd-1", "status": "completed", "success": true}`
			case r.URL.Path == "/api/webftp/downloads/t-1/status/":
				return http.StatusOK, `{"success": true, "message": "done"}`
			case r.URL.Path == "/blob":
				return status, body
			default:
				return http.StatusOK, `{}`
			}
		}),
	}
}

// A hinted fetch refusal keeps its hint instead of using the download checklist,
// including a 429 that outlasts retries.
func TestDownloadObject_FetchRefusalWithAHintSkipsTheChecklist(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		body   string
		hint   string
	}{
		{
			name:   "a file access refusal",
			status: http.StatusForbidden,
			body:   `{"code": "api_token_acl_not_allowed", "gate": "file_acl", "hint": "This token's file access rules do not allow this transfer for this path, account and direction."}`,
			hint:   "This token's file access rules do not allow this transfer for this path, account and direction.",
		},
		{
			name:   "a throttle that outlasts the retries",
			status: http.StatusTooManyRequests,
			body:   `{"code": "api_rate_limited", "gate": "rate", "hint": "Too many requests were made in a short time; wait, then retry."}`,
			hint:   "Too many requests were made in a short time; wait, then retry.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ac := refusedDownloadClient(tt.status, tt.body)
			dest := t.TempDir()

			var serverName string
			var err error
			// The fake clock spends the retry backoff at once.
			synctest.Test(t, func(t *testing.T) {
				serverName, err = downloadObject(ac, []string{"my-server:/home/alice/a.txt"}, dest, "", "", false, "")
			})
			require.Error(t, err)
			assert.Equal(t, "my-server", serverName)
			assert.Contains(t, err.Error(), "download failed")
			assert.Equal(t, tt.hint, utils.RefusalHint(err))
		})
	}
}

type createdDownload struct{ Username, Groupname string }

// downloadClientAnswering serves a download that answers path with status and body; an empty
// path fails no request.
func downloadClientAnswering(t *testing.T, path string, status int, body string) (*client.AlpaconClient, *createdDownload) {
	t.Helper()
	created := &createdDownload{}
	return &client.AlpaconClient{
		BaseURL:       testutil.StubBaseURL,
		WorkspaceName: "my-workspace",
		HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
			switch {
			case r.URL.Path == path:
				return status, body
			case r.URL.Path == "/api/servers/servers/":
				return http.StatusOK, `{"count": 1, "results": [{"id": "srv-id", "name": "my-server"}]}`
			case r.Method == http.MethodPost && (r.URL.Path == "/api/webftp/downloads/" || r.URL.Path == "/api/webftp/downloads/bulk/"):
				if err := json.NewDecoder(r.Body).Decode(created); err != nil {
					t.Errorf("decode the download create: %v", err)
				}
				return http.StatusCreated, `{"id": "t-1", "command": "cmd-1", "download_url": "http://stub.invalid/blob"}`
			case r.URL.Path == "/api/events/commands/cmd-1/":
				return http.StatusOK, `{"id": "cmd-1", "status": "completed", "success": true}`
			case r.URL.Path == "/api/webftp/downloads/t-1/status/":
				return http.StatusOK, `{"success": true, "message": "done"}`
			case r.URL.Path == "/blob":
				return http.StatusOK, "content"
			default:
				return http.StatusNotImplemented, `{}`
			}
		}),
	}, created
}

func TestDownloadObject_SingleFileArrivesInDest(t *testing.T) {
	t.Parallel()
	ac, created := downloadClientAnswering(t, "", 0, "")
	dest := t.TempDir()

	var serverName string
	var err error
	synctest.Test(t, func(t *testing.T) {
		serverName, err = downloadObject(ac, []string{"my-server:/tmp/a.txt"}, dest, "alice", "staff", false, "")
	})

	require.NoError(t, err)
	assert.Equal(t, "my-server", serverName)
	assert.Equal(t, createdDownload{Username: "alice", Groupname: "staff"}, *created)
	got, readErr := os.ReadFile(filepath.Join(dest, "a.txt"))
	require.NoError(t, readErr)
	assert.Equal(t, "content", string(got))
}

func TestDownloadObject_FetchNotFoundNamesTheRemotePathInsteadOfTheGenericChecklist(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                string
		remotePath          string
		username, groupname string
		recursive           bool
		entry               string
		accountLine         string
	}{
		{
			name:        "no account flags",
			remotePath:  "/tmp/no-such-file.txt",
			entry:       "file",
			accountLine: "Your account can read the file, or name one that can with -u and -g",
		},
		{
			name:        "both account flags",
			remotePath:  "/tmp/no-such-file.txt",
			entry:       "file",
			username:    "alice",
			groupname:   "staff",
			accountLine: "The account the transfer ran as (user 'alice', group 'staff') exists on the server and can read the file",
		},
		{
			name:        "only a group",
			remotePath:  "/tmp/no-such-file.txt",
			entry:       "file",
			groupname:   "staff",
			accountLine: "The account the transfer ran as (group 'staff') exists on the server and can read the file",
		},
		{
			name:        "recursive folder",
			remotePath:  "/tmp/no-such-dir",
			recursive:   true,
			entry:       "folder",
			accountLine: "Your account can read the folder, or name one that can with -u and -g",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ac, created := downloadClientAnswering(t, "/blob", http.StatusNotFound, storageNotFoundBody)
			dest := t.TempDir()

			var serverName string
			var err error
			synctest.Test(t, func(t *testing.T) { // the fake clock spends the command poll's wait at once
				serverName, err = downloadObject(ac, []string{"my-server:" + tt.remotePath}, dest, tt.username, tt.groupname, tt.recursive, "")
			})

			assert.Equal(t, "my-server", serverName)
			assert.Equal(t, createdDownload{Username: tt.username, Groupname: tt.groupname}, *created)
			var exitErr *utils.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, utils.ExitCodeGeneralError, exitErr.Code)
			ios, _, _ := utils.TestIOStreams()
			var output bytes.Buffer
			ios.FprintExitError(&output, exitErr)
			assert.Contains(t, output.String(), "'"+tt.remotePath+"' was not found on server 'my-server', or the transfer's account could not read it.\n\n"+
				"Please check:\n"+
				"  • The path is correct and the "+tt.entry+" exists\n"+
				"  • "+tt.accountLine+"\n")
			assert.NotContains(t, output.String(), "This might be due to")
		})
	}
}

func TestDownloadObject_FetchNotFoundKeepsItsStatusInTheJSONEnvelope(t *testing.T) {
	t.Parallel()
	ac, _ := downloadClientAnswering(t, "/blob", http.StatusNotFound, storageNotFoundBody)
	dest := t.TempDir()

	var err error
	synctest.Test(t, func(t *testing.T) {
		_, err = downloadObject(ac, []string{"my-server:/tmp/no-such-file.txt"}, dest, "", "", false, "")
	})

	var exitErr *utils.ExitError
	require.ErrorAs(t, err, &exitErr)
	ios, _, _ := utils.TestIOStreams()
	require.NoError(t, ios.SetOutputFormat(utils.OutputFormatJSON))
	var output bytes.Buffer
	ios.FprintExitError(&output, exitErr)
	var envelope map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &envelope), "output: %s", output.String())
	assert.Equal(t, "http_404", envelope["error_code"])
	assert.InDelta(t, http.StatusNotFound, envelope["http_status"], 0)
	assert.Contains(t, envelope["message"], "'/tmp/no-such-file.txt' was not found on server 'my-server'")
}

func TestDownloadObject_NotFoundFromAnotherRequestIsNotReportedAsAMissingFile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, path, body string
	}{
		{name: "the download create", path: "/api/webftp/downloads/", body: `{"code": "not_found"}`},
		{name: "the download create saying the download failed", path: "/api/webftp/downloads/", body: `{"detail": "download failed"}`},
		{name: "the command poll", path: "/api/events/commands/cmd-1/", body: `{"code": "not_found"}`},
		{name: "the transfer status after the file arrived", path: "/api/webftp/downloads/t-1/status/", body: `{"detail": "download failed"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ac, _ := downloadClientAnswering(t, tt.path, http.StatusNotFound, tt.body)
			dest := t.TempDir()

			var err error
			synctest.Test(t, func(t *testing.T) {
				_, err = downloadObject(ac, []string{"my-server:/tmp/a.txt"}, dest, "", "", false, "")
			})

			require.Error(t, err)
			assert.Equal(t, http.StatusNotFound, utils.HTTPStatusCode(err), "the 404 must come from %s: %v", tt.path, err)
			assert.NotContains(t, err.Error(), "was not found on server")
		})
	}
}

// A bulk archive names no single path, and another status, such as an expired URL's 403, says
// nothing about the file.
func TestDownloadObject_OtherStorageRefusalsKeepTheGenericChecklist(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		sources []string
		status  int
		body    string
	}{
		{
			name:    "a bulk archive that is not found",
			sources: []string{"my-server:/tmp/a.txt", "my-server:/tmp/b.txt"},
			status:  http.StatusNotFound,
			body:    storageNotFoundBody,
		},
		{
			name:    "an expired download URL",
			sources: []string{"my-server:/tmp/a.txt"},
			status:  http.StatusForbidden,
			body:    `<?xml version="1.0" encoding="UTF-8"?><Error><Code>AccessDenied</Code><Message>Request has expired</Message></Error>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ac, _ := downloadClientAnswering(t, "/blob", tt.status, tt.body)
			dest := t.TempDir()

			var err error
			synctest.Test(t, func(t *testing.T) {
				_, err = downloadObject(ac, tt.sources, dest, "", "", false, "")
			})

			var exitErr *utils.ExitError
			require.ErrorAs(t, err, &exitErr)
			ios, _, _ := utils.TestIOStreams()
			var output bytes.Buffer
			ios.FprintExitError(&output, exitErr)
			assert.Contains(t, output.String(), fmt.Sprintf("download failed with client error: %d\n\nThis might be due to", tt.status))
			assert.NotContains(t, output.String(), "was not found on server")
		})
	}
}

func TestDownloadObject_DeniedCommandSkipsTheChecklists(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, result string }{
		{"permission wording", "permission denied by policy"},
		{"missing file wording", "file not found for this account"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			detail := fmt.Sprintf(`{"id": "cmd-1", "status": "denied", "denial_code": "future_reason", "result": %q}`, tc.result)
			ac := &client.AlpaconClient{
				BaseURL:       testutil.StubBaseURL,
				WorkspaceName: "my-workspace",
				HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
					switch {
					case r.URL.Path == "/api/servers/servers/":
						return http.StatusOK, `{"count": 1, "results": [{"id": "srv-id", "name": "my-server"}]}`
					case r.Method == http.MethodPost && r.URL.Path == "/api/webftp/downloads/":
						return http.StatusCreated, `{"id": "t-1", "command": "cmd-1", "download_url": "http://stub.invalid/blob"}`
					case r.URL.Path == "/api/events/commands/cmd-1/":
						return http.StatusOK, detail
					default:
						return http.StatusOK, `{}`
					}
				}),
			}

			var err error
			synctest.Test(t, func(t *testing.T) {
				_, err = downloadObject(ac, []string{"my-server:/home/alice/a.txt"}, t.TempDir(), "", "", false, "")
			})

			var rejected *event.CommandRejectedError
			require.ErrorAs(t, err, &rejected)
			assert.Equal(t, utils.ExitCodeNotApproved, rejected.ExitCode())
		})
	}
}

func TestReportDownloadRejection_LeavesOtherFailuresToTheCaller(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "success"},
		{name: "network failure", err: errors.New("connection reset")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := reportDownloadRejection(tc.err, "[prod:/a]")

			assert.NoError(t, err)
		})
	}
}

func TestUploadObject_ReturnsTheMissingSourceAdvice(t *testing.T) {
	t.Parallel()
	ac := &client.AlpaconClient{
		BaseURL: testutil.StubBaseURL,
		HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
			return http.StatusOK, `{"count":1,"results":[{"id":"server","name":"my-server"}]}`
		}),
	}
	source := t.TempDir() + "/missing.txt"

	server, err := uploadObject(ac, []string{source}, "my-server:/tmp/", "", "", false, true, "")

	var exitErr *utils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, "my-server", server)
	assert.Equal(t, utils.ExitCodeGeneralError, exitErr.Code)
	var output bytes.Buffer
	utils.FprintExitError(&output, exitErr)
	assert.Contains(t, output.String(), "Source file(s) not found: "+source)
	assert.NotContains(t, output.String(), "Failed to upload to")
}
