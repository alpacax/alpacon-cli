package edit

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	ftpapi "github.com/alpacax/alpacon-cli/api/ftp"
	"github.com/alpacax/alpacon-cli/api/types"
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type editGateError struct{}

func (editGateError) Error() string       { return "work session required" }
func (editGateError) ErrorCode() string   { return utils.WorkSessionRequired }
func (editGateError) ErrorSource() string { return "" }

func TestEditDownloadResendsWithTheCreatedWorkSession(t *testing.T) {
	var sentIDs []string
	var create wsapi.WorkSessionCreateRequest
	ac, ios, _ := setupEditOffer(t, &create)
	downloadEditFile = func(_ *client.AlpaconClient, _, _, localPath, _, _, id string) (ftpapi.DownloadedFile, error) {
		sentIDs = append(sentIDs, id)
		if len(sentIDs) == 1 {
			return ftpapi.DownloadedFile{}, utils.MarkSubmission(editGateError{})
		}
		require.NoError(t, os.WriteFile(localPath, []byte("original"), 0600))
		return ftpapi.DownloadedFile{Path: localPath, Size: int64(len("original"))}, nil
	}
	uploadEditFile = func(*client.AlpaconClient, string, string, string, string, string, string) error {
		t.Fatal("an unchanged download must not upload")
		return nil
	}
	localEditorRunner = func(string, string) error { return nil }

	cmd := NewEditCmd(func() (*client.AlpaconClient, error) { return ac, nil }, ios)
	cmd.SetArgs([]string{"web-1:/etc/app.conf"})
	err := cmd.Execute()

	require.NoError(t, err)
	assert.Equal(t, []string{"old-session", "new-session"}, sentIDs)
	assert.Equal(t, []string{utils.OperationWebFTP}, create.Scopes)
	assert.Equal(t, []string{"server-1"}, create.Servers)
}

func TestEditUploadResendsWithTheCreatedWorkSession(t *testing.T) {
	var downloadIDs, uploadIDs []string
	var create wsapi.WorkSessionCreateRequest
	ac, ios, _ := setupEditOffer(t, &create)
	downloadEditFile = func(_ *client.AlpaconClient, _, _, localPath, _, _, id string) (ftpapi.DownloadedFile, error) {
		downloadIDs = append(downloadIDs, id)
		require.NoError(t, os.WriteFile(localPath, []byte("original"), 0600))
		return ftpapi.DownloadedFile{Path: localPath, Size: int64(len("original"))}, nil
	}
	localEditorRunner = func(_, path string) error {
		return os.WriteFile(path, []byte("changed"), 0600)
	}
	uploadEditFile = func(_ *client.AlpaconClient, _, _, _, _, _, id string) error {
		uploadIDs = append(uploadIDs, id)
		if len(uploadIDs) == 1 {
			return utils.MarkSubmission(editGateError{})
		}
		return nil
	}

	cmd := NewEditCmd(func() (*client.AlpaconClient, error) { return ac, nil }, ios)
	cmd.SetArgs([]string{"web-1:/etc/app.conf"})
	err := cmd.Execute()

	require.NoError(t, err)
	assert.Equal(t, []string{"old-session"}, downloadIDs)
	assert.Equal(t, []string{"old-session", "new-session"}, uploadIDs)
	assert.Equal(t, []string{utils.OperationWebFTP}, create.Scopes)
	assert.Equal(t, []string{"server-1"}, create.Servers)
}

func TestEditUploadRecoveryFailurePrintsThePreservedPath(t *testing.T) {
	var create wsapi.WorkSessionCreateRequest
	ac, ios, stderr := setupEditOffer(t, &create)
	downloadEditFile = func(_ *client.AlpaconClient, _, _, localPath, _, _, _ string) (ftpapi.DownloadedFile, error) {
		require.NoError(t, os.WriteFile(localPath, []byte("original"), 0600))
		return ftpapi.DownloadedFile{Path: localPath, Size: int64(len("original"))}, nil
	}
	localEditorRunner = func(_, path string) error {
		return os.WriteFile(path, []byte("changed"), 0600)
	}
	attempts := 0
	uploadEditFile = func(*client.AlpaconClient, string, string, string, string, string, string) error {
		attempts++
		if attempts == 1 {
			return utils.MarkSubmission(editGateError{})
		}
		return utils.NewExitError("recovery failed")
	}

	cmd := NewEditCmd(func() (*client.AlpaconClient, error) { return ac, nil }, ios)
	cmd.SetArgs([]string{"web-1:/etc/app.conf"})
	err := cmd.Execute()

	var exitErr *utils.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, utils.ExitCodeGeneralError, exitErr.Code)
	assert.Contains(t, stderr.String(), "Edited file preserved at ")
}

func setupEditOffer(t *testing.T, create *wsapi.WorkSessionCreateRequest) (*client.AlpaconClient, *utils.IOStreams, fmt.Stringer) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	require.NoError(t, config.CreateConfig("https://workspace.example", "workspace", "", "", "", "", "", 0, false))
	replaceEditStdin(t, "y\nedit a file\n1h\n")
	ios, _, stderr := utils.TestIOStreams()
	active := wsapi.WorkSession{
		ID:            "new-session",
		Status:        wsapi.StatusActive,
		RequesterType: "user",
		Scopes:        []string{utils.OperationWebFTP},
		Servers:       []types.ServerSummary{{ID: "server-1", Name: "web-1"}},
		AssignedUser:  &types.UserSummary{ID: "user-1"},
		ExpiresAt:     time.Now().Add(time.Hour),
	}
	activeJSON, err := json.Marshal(active)
	require.NoError(t, err)
	ac := &client.AlpaconClient{
		BaseURL:       testutil.StubBaseURL,
		WorkspaceName: "workspace",
		HTTPClient: testutil.StubClient(func(r *http.Request) (int, string) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/api/iam/users/-/":
				return http.StatusOK, `{"id":"user-1"}`
			case r.Method == http.MethodGet && r.URL.Path == "/api/servers/servers/":
				return http.StatusOK, `{"count":1,"results":[{"id":"server-1","name":"web-1"}]}`
			case r.Method == http.MethodGet && r.URL.Path == "/api/work-sessions/sessions/":
				return http.StatusOK, `{"count":0,"results":[]}`
			case r.Method == http.MethodPost && r.URL.Path == "/api/work-sessions/sessions/":
				body, readErr := io.ReadAll(r.Body)
				require.NoError(t, readErr)
				require.NoError(t, json.Unmarshal(body, create))
				return http.StatusCreated, string(activeJSON)
			case r.Method == http.MethodGet && r.URL.Path == "/api/work-sessions/sessions/new-session/":
				return http.StatusOK, string(activeJSON)
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
				return http.StatusInternalServerError, `{}`
			}
		}),
	}
	originalSelect := selectWorkSession
	originalDownload := downloadEditFile
	originalUpload := uploadEditFile
	originalEditor := localEditorRunner
	selectWorkSession = func(gotIOS *utils.IOStreams, gotAC *client.AlpaconClient, _ string, need worksession.Need) (*worksession.Selection, error) {
		assert.Equal(t, worksession.Transfer, need.Operation)
		return worksession.OfferedForTest(gotIOS, gotAC, "old-session", need), nil
	}
	t.Cleanup(func() {
		selectWorkSession = originalSelect
		downloadEditFile = originalDownload
		uploadEditFile = originalUpload
		localEditorRunner = originalEditor
	})
	return ac, ios, stderr
}

func replaceEditStdin(t *testing.T, input string) {
	t.Helper()
	read, write, err := os.Pipe()
	require.NoError(t, err)
	_, err = write.WriteString(input)
	require.NoError(t, err)
	require.NoError(t, write.Close())
	original := os.Stdin
	os.Stdin = read
	t.Cleanup(func() {
		os.Stdin = original
		_ = read.Close()
	})
}

func TestResolveEditor(t *testing.T) {
	t.Setenv("ALPACON_EDITOR", "zed --wait")
	t.Setenv("VISUAL", "vim")
	t.Setenv("EDITOR", "nano")
	assert.Equal(t, "code --wait", resolveEditor("code --wait"))
	assert.Equal(t, "zed --wait", resolveEditor(""))

	t.Setenv("ALPACON_EDITOR", "")
	assert.Equal(t, "vim", resolveEditor(""))

	t.Setenv("VISUAL", "")
	assert.Equal(t, "nano", resolveEditor(""))

	t.Setenv("EDITOR", "")
	assert.Equal(t, "vi", resolveEditor(""))
}

func TestRunLocalEditorSupportsQuotedArguments(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "args.log")
	scriptPath := filepath.Join(dir, "editor.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$1\"\n"), 0700))
	targetPath := filepath.Join(dir, "target file.txt")
	require.NoError(t, os.WriteFile(targetPath, []byte("content"), 0600))

	err := runLocalEditor(scriptPath+" "+logPath+" \"two words\"", targetPath)
	require.NoError(t, err)

	got, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Equal(t, logPath+"\ntwo words\n"+targetPath+"\n", string(got))
}

func TestSplitEditorCommandPreservesWindowsBackslashes(t *testing.T) {
	t.Parallel()
	parts, err := splitEditorCommand(`"C:\Program Files\Vim\vim.exe" --wait`)
	require.NoError(t, err)
	assert.Equal(t, []string{`C:\Program Files\Vim\vim.exe`, "--wait"}, parts)
}

func TestSplitEditorCommandPreservesEmptyQuotedArgument(t *testing.T) {
	t.Parallel()
	parts, err := splitEditorCommand(`emacsclient -a "" -c`)
	require.NoError(t, err)
	assert.Equal(t, []string{"emacsclient", "-a", "", "-c"}, parts)
}

func TestGuiEditorWaitWarning(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
		warn  bool
	}{
		{name: "gui editor without wait flag", input: "code", warn: true},
		{name: "gui editor full path without wait flag", input: "/usr/local/bin/code", warn: true},
		{name: "gui editor with --wait", input: "code --wait", warn: false},
		{name: "gui editor with -w", input: "subl -w", warn: false},
		{name: "terminal editor", input: "vim", warn: false},
		{name: "terminal editor with args", input: "nano -w", warn: false},
		{name: "empty command", input: "", warn: false},
		{name: "unmatched quote", input: `code "--wait`, warn: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			warning := guiEditorWaitWarning(tc.input)
			if tc.warn {
				assert.NotEmpty(t, warning)
			} else {
				assert.Empty(t, warning)
			}
		})
	}
}

func TestParseEditTarget(t *testing.T) {
	t.Parallel()
	target, err := parseEditTarget("root@prod:/etc/nginx/nginx.conf", "")
	require.NoError(t, err)
	assert.Equal(t, "prod", target.Server)
	assert.Equal(t, "/etc/nginx/nginx.conf", target.RemotePath)
	assert.Equal(t, "root", target.Username)

	target, err = parseEditTarget("h:/etc/app.conf", "")
	require.NoError(t, err)
	assert.Equal(t, "h", target.Server)
	assert.Equal(t, "/etc/app.conf", target.RemotePath)

	target, err = parseEditTarget("root@prod:/etc/nginx/nginx.conf", "deploy")
	require.NoError(t, err)
	assert.Equal(t, "deploy", target.Username)

	target, err = parseEditTarget("prod:/tmp/alice@example.com", "")
	require.NoError(t, err)
	assert.Equal(t, "prod", target.Server)
	assert.Equal(t, "/tmp/alice@example.com", target.RemotePath)
	assert.Empty(t, target.Username)
}

func TestParseEditTargetRejectsInvalidTargets(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input string
	}{
		{name: "no colon", input: "prod"},
		{name: "missing path", input: "prod:"},
		{name: "missing server", input: ":/etc/app.conf"},
		{name: "user only without path", input: "root@prod"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseEditTarget(tc.input, "")
			require.Error(t, err)
		})
	}
}

func TestEditTempPathUniquePerInvocation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := editTarget{Server: "prod", RemotePath: "/etc/app.conf"}
	first, err := editTempPath(root, target)
	require.NoError(t, err)
	second, err := editTempPath(root, target)
	require.NoError(t, err)
	assert.NotEqual(t, first, second)
	assert.Equal(t, "app.conf", filepath.Base(first))
	assert.Equal(t, "app.conf", filepath.Base(second))
}

func TestEditTempPathRejectsInvalidRemoteBasename(t *testing.T) {
	t.Parallel()
	for _, remotePath := range []string{"/tmp/..", "/tmp/app.conf/", `/tmp/..\saved`} {
		t.Run(remotePath, func(t *testing.T) {
			_, err := editTempPath(t.TempDir(), editTarget{Server: "prod", RemotePath: remotePath})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "file name")
		})
	}
}

func TestRunEditRestrictsDownloadedFilePermissions(t *testing.T) {
	t.Parallel()
	tempRoot := t.TempDir()
	var editorPerm os.FileMode
	deps := editDeps{
		download: func(target editTarget, localPath string) (ftpapi.DownloadedFile, error) {
			require.NoError(t, os.MkdirAll(filepath.Dir(localPath), 0700))
			require.NoError(t, os.WriteFile(localPath, []byte("secret"), 0644))
			return ftpapi.DownloadedFile{Path: localPath, Size: int64(len("secret"))}, nil
		},
		upload: func(target editTarget, localPath string) error {
			return nil
		},
		runEditor: func(editor, filePath string) error {
			info, err := os.Stat(filePath)
			if err != nil {
				return err
			}
			editorPerm = info.Mode().Perm()
			return nil
		},
		confirmLarge: func(size int64) bool {
			return true
		},
		tempRoot: tempRoot,
	}

	_, err := runEdit(utils.Stdio, editOptions{
		Target: editTarget{Server: "prod", RemotePath: "/etc/secret.conf"},
		Editor: "true",
	}, deps)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), editorPerm, "downloaded file should be restricted to the owner before editing")
}

func TestRunEditNoChangesSkipsUploadAndRemovesTemp(t *testing.T) {
	t.Parallel()
	tempRoot := t.TempDir()
	var uploadCalled bool
	deps := editDeps{
		download: func(target editTarget, localPath string) (ftpapi.DownloadedFile, error) {
			require.NoError(t, os.MkdirAll(filepath.Dir(localPath), 0700))
			require.NoError(t, os.WriteFile(localPath, []byte("original"), 0600))
			return ftpapi.DownloadedFile{Path: localPath, Size: int64(len("original"))}, nil
		},
		upload: func(target editTarget, localPath string) error {
			uploadCalled = true
			return nil
		},
		runEditor: func(editor, filePath string) error {
			return os.WriteFile(filepath.Join(filepath.Dir(filePath), ".app.conf.swp"), []byte("sidecar"), 0600)
		},
		confirmLarge: func(size int64) bool {
			return true
		},
		tempRoot: tempRoot,
	}

	result, err := runEdit(utils.Stdio, editOptions{
		Target: editTarget{Server: "prod", RemotePath: "/etc/app.conf"},
		Editor: "true",
	}, deps)
	require.NoError(t, err)
	assert.False(t, result.Changed)
	assert.False(t, uploadCalled)
	_, statErr := os.Stat(result.TempPath)
	assert.True(t, os.IsNotExist(statErr), "unchanged edit should remove temp file")
	_, dirErr := os.Stat(filepath.Dir(result.TempPath))
	assert.True(t, os.IsNotExist(dirErr), "unchanged edit should remove temp directory")
}

func TestRunEditSuccessfulUploadRemovesEditorSidecarFiles(t *testing.T) {
	t.Parallel()
	tempRoot := t.TempDir()
	deps := editDeps{
		download: func(target editTarget, localPath string) (ftpapi.DownloadedFile, error) {
			require.NoError(t, os.MkdirAll(filepath.Dir(localPath), 0700))
			require.NoError(t, os.WriteFile(localPath, []byte("original"), 0600))
			return ftpapi.DownloadedFile{Path: localPath, Size: int64(len("original"))}, nil
		},
		upload: func(target editTarget, localPath string) error {
			return nil
		},
		runEditor: func(editor, filePath string) error {
			require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(filePath), ".app.conf.swp"), []byte("sidecar"), 0600))
			return os.WriteFile(filePath, []byte("changed"), 0600)
		},
		confirmLarge: func(size int64) bool {
			return true
		},
		tempRoot: tempRoot,
	}

	result, err := runEdit(utils.Stdio, editOptions{
		Target: editTarget{Server: "prod", RemotePath: "/etc/app.conf"},
		Editor: "true",
	}, deps)
	require.NoError(t, err)
	assert.True(t, result.Changed)
	_, statErr := os.Stat(result.TempPath)
	assert.True(t, os.IsNotExist(statErr), "successful edit should remove temp file")
	_, dirErr := os.Stat(filepath.Dir(result.TempPath))
	assert.True(t, os.IsNotExist(dirErr), "successful edit should remove temp directory")
}

func TestRunEditUploadFailurePreservesTempPath(t *testing.T) {
	t.Parallel()
	tempRoot := t.TempDir()
	uploadErr := errors.New("upload failed")
	deps := editDeps{
		download: func(target editTarget, localPath string) (ftpapi.DownloadedFile, error) {
			require.NoError(t, os.MkdirAll(filepath.Dir(localPath), 0700))
			require.NoError(t, os.WriteFile(localPath, []byte("original"), 0600))
			return ftpapi.DownloadedFile{Path: localPath, Size: int64(len("original"))}, nil
		},
		upload: func(target editTarget, localPath string) error {
			return uploadErr
		},
		runEditor: func(editor, filePath string) error {
			return os.WriteFile(filePath, []byte("changed"), 0600)
		},
		confirmLarge: func(size int64) bool {
			return true
		},
		tempRoot: tempRoot,
	}

	result, err := runEdit(utils.Stdio, editOptions{
		Target: editTarget{Server: "prod", RemotePath: "/etc/app.conf"},
		Editor: "true",
	}, deps)
	require.ErrorIs(t, err, uploadErr)
	assert.True(t, result.Changed)
	content, readErr := os.ReadFile(result.TempPath)
	require.NoError(t, readErr)
	assert.Equal(t, "changed", string(content))
}

func TestRunEditEditorFailureWithoutChangeRemovesTemp(t *testing.T) {
	t.Parallel()
	tempRoot := t.TempDir()
	editorErr := errors.New("editor exited 1")
	deps := editDeps{
		download: func(target editTarget, localPath string) (ftpapi.DownloadedFile, error) {
			require.NoError(t, os.MkdirAll(filepath.Dir(localPath), 0700))
			require.NoError(t, os.WriteFile(localPath, []byte("original"), 0600))
			return ftpapi.DownloadedFile{Path: localPath, Size: int64(len("original"))}, nil
		},
		upload: func(target editTarget, localPath string) error {
			t.Fatal("upload should not be called")
			return nil
		},
		runEditor: func(editor, filePath string) error {
			return editorErr
		},
		confirmLarge: func(size int64) bool { return true },
		tempRoot:     tempRoot,
	}

	result, err := runEdit(utils.Stdio, editOptions{
		Target: editTarget{Server: "prod", RemotePath: "/etc/app.conf"},
		Editor: "true",
	}, deps)
	require.ErrorIs(t, err, editorErr)
	assert.False(t, result.Changed)
	_, statErr := os.Stat(result.TempPath)
	assert.True(t, os.IsNotExist(statErr), "editor failure without changes should remove temp file")
}

func TestRunEditEditorFailureAfterChangePreservesTemp(t *testing.T) {
	t.Parallel()
	tempRoot := t.TempDir()
	editorErr := errors.New("editor exited 1")
	deps := editDeps{
		download: func(target editTarget, localPath string) (ftpapi.DownloadedFile, error) {
			require.NoError(t, os.MkdirAll(filepath.Dir(localPath), 0700))
			require.NoError(t, os.WriteFile(localPath, []byte("original"), 0600))
			return ftpapi.DownloadedFile{Path: localPath, Size: int64(len("original"))}, nil
		},
		upload: func(target editTarget, localPath string) error {
			t.Fatal("upload should not be called")
			return nil
		},
		runEditor: func(editor, filePath string) error {
			require.NoError(t, os.WriteFile(filePath, []byte("changed"), 0600))
			return editorErr
		},
		confirmLarge: func(size int64) bool { return true },
		tempRoot:     tempRoot,
	}

	result, err := runEdit(utils.Stdio, editOptions{
		Target: editTarget{Server: "prod", RemotePath: "/etc/app.conf"},
		Editor: "true",
	}, deps)
	require.ErrorIs(t, err, editorErr)
	assert.True(t, result.Changed)
	content, readErr := os.ReadFile(result.TempPath)
	require.NoError(t, readErr)
	assert.Equal(t, "changed", string(content), "editor failure after edits should preserve the changed temp file")
}

func TestRunEditLargeFileDeclineRemovesTempAndSkipsEditor(t *testing.T) {
	t.Parallel()
	tempRoot := t.TempDir()
	var editorCalled bool
	deps := editDeps{
		download: func(target editTarget, localPath string) (ftpapi.DownloadedFile, error) {
			require.NoError(t, os.MkdirAll(filepath.Dir(localPath), 0700))
			require.NoError(t, os.WriteFile(localPath, []byte("large"), 0600))
			return ftpapi.DownloadedFile{Path: localPath, Size: maxEditPromptSize + 1}, nil
		},
		upload: func(target editTarget, localPath string) error {
			t.Fatal("upload should not be called")
			return nil
		},
		runEditor: func(editor, filePath string) error {
			editorCalled = true
			return nil
		},
		confirmLarge: func(size int64) bool {
			return false
		},
		tempRoot: tempRoot,
	}

	result, err := runEdit(utils.Stdio, editOptions{
		Target: editTarget{Server: "prod", RemotePath: "/var/log/big.log"},
		Editor: "true",
	}, deps)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--force")
	assert.False(t, editorCalled)
	_, statErr := os.Stat(result.TempPath)
	assert.True(t, os.IsNotExist(statErr), "declined large file edit should remove temp file")
	_, dirErr := os.Stat(filepath.Dir(result.TempPath))
	assert.True(t, os.IsNotExist(dirErr), "declined large file edit should remove temp directory")
}

func TestRefusedForWantOfForce(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		err         error
		interactive bool
		want        bool
	}{
		{name: "large file without a terminal", err: errLargeFile, want: true},
		{name: "large file wrapped without a terminal", err: fmt.Errorf("edit: %w", errLargeFile), want: true},
		{name: "large file declined at a prompt", err: errLargeFile, interactive: true},
		{name: "other failure without a terminal", err: errors.New("boom")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, refusedForWantOfForce(tt.err, tt.interactive))
		})
	}
}
