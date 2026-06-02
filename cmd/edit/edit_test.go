package edit

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	ftpapi "github.com/alpacax/alpacon-cli/api/ftp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	parts, err := splitEditorCommand(`"C:\Program Files\Vim\vim.exe" --wait`)
	require.NoError(t, err)
	assert.Equal(t, []string{`C:\Program Files\Vim\vim.exe`, "--wait"}, parts)
}

func TestSplitEditorCommandPreservesEmptyQuotedArgument(t *testing.T) {
	parts, err := splitEditorCommand(`emacsclient -a "" -c`)
	require.NoError(t, err)
	assert.Equal(t, []string{"emacsclient", "-a", "", "-c"}, parts)
}

func TestGuiEditorWaitWarning(t *testing.T) {
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
	target, err := parseEditTarget("root@prod:/etc/nginx/nginx.conf", "")
	require.NoError(t, err)
	assert.Equal(t, "prod", target.Server)
	assert.Equal(t, "/etc/nginx/nginx.conf", target.RemotePath)
	assert.Equal(t, "root", target.Username)

	target, err = parseEditTarget("root@prod:/etc/nginx/nginx.conf", "deploy")
	require.NoError(t, err)
	assert.Equal(t, "deploy", target.Username)

	target, err = parseEditTarget("prod:/tmp/alice@example.com", "")
	require.NoError(t, err)
	assert.Equal(t, "prod", target.Server)
	assert.Equal(t, "/tmp/alice@example.com", target.RemotePath)
	assert.Equal(t, "", target.Username)
}

func TestParseEditTargetRejectsInvalidTargets(t *testing.T) {
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
	for _, remotePath := range []string{"/tmp/..", "/tmp/app.conf/", `/tmp/..\saved`} {
		t.Run(remotePath, func(t *testing.T) {
			_, err := editTempPath(t.TempDir(), editTarget{Server: "prod", RemotePath: remotePath})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "file name")
		})
	}
}

func TestRunEditRestrictsDownloadedFilePermissions(t *testing.T) {
	tempRoot := t.TempDir()
	var editorPerm os.FileMode
	deps := editDeps{
		download: func(target editTarget, localPath, workSessionID string) (ftpapi.DownloadedFile, error) {
			require.NoError(t, os.MkdirAll(filepath.Dir(localPath), 0700))
			require.NoError(t, os.WriteFile(localPath, []byte("secret"), 0644))
			return ftpapi.DownloadedFile{Path: localPath, Size: int64(len("secret"))}, nil
		},
		upload: func(target editTarget, localPath, workSessionID string) error {
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

	_, err := runEdit(editOptions{
		Target: editTarget{Server: "prod", RemotePath: "/etc/secret.conf"},
		Editor: "true",
	}, deps)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), editorPerm, "downloaded file should be restricted to the owner before editing")
}

func TestRunEditNoChangesSkipsUploadAndRemovesTemp(t *testing.T) {
	tempRoot := t.TempDir()
	var uploadCalled bool
	deps := editDeps{
		download: func(target editTarget, localPath, workSessionID string) (ftpapi.DownloadedFile, error) {
			require.NoError(t, os.MkdirAll(filepath.Dir(localPath), 0700))
			require.NoError(t, os.WriteFile(localPath, []byte("original"), 0600))
			return ftpapi.DownloadedFile{Path: localPath, Size: int64(len("original"))}, nil
		},
		upload: func(target editTarget, localPath, workSessionID string) error {
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

	result, err := runEdit(editOptions{
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
	tempRoot := t.TempDir()
	deps := editDeps{
		download: func(target editTarget, localPath, workSessionID string) (ftpapi.DownloadedFile, error) {
			require.NoError(t, os.MkdirAll(filepath.Dir(localPath), 0700))
			require.NoError(t, os.WriteFile(localPath, []byte("original"), 0600))
			return ftpapi.DownloadedFile{Path: localPath, Size: int64(len("original"))}, nil
		},
		upload: func(target editTarget, localPath, workSessionID string) error {
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

	result, err := runEdit(editOptions{
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
	tempRoot := t.TempDir()
	uploadErr := errors.New("upload failed")
	deps := editDeps{
		download: func(target editTarget, localPath, workSessionID string) (ftpapi.DownloadedFile, error) {
			require.NoError(t, os.MkdirAll(filepath.Dir(localPath), 0700))
			require.NoError(t, os.WriteFile(localPath, []byte("original"), 0600))
			return ftpapi.DownloadedFile{Path: localPath, Size: int64(len("original"))}, nil
		},
		upload: func(target editTarget, localPath, workSessionID string) error {
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

	result, err := runEdit(editOptions{
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
	tempRoot := t.TempDir()
	editorErr := errors.New("editor exited 1")
	deps := editDeps{
		download: func(target editTarget, localPath, workSessionID string) (ftpapi.DownloadedFile, error) {
			require.NoError(t, os.MkdirAll(filepath.Dir(localPath), 0700))
			require.NoError(t, os.WriteFile(localPath, []byte("original"), 0600))
			return ftpapi.DownloadedFile{Path: localPath, Size: int64(len("original"))}, nil
		},
		upload: func(target editTarget, localPath, workSessionID string) error {
			t.Fatal("upload should not be called")
			return nil
		},
		runEditor: func(editor, filePath string) error {
			return editorErr
		},
		confirmLarge: func(size int64) bool { return true },
		tempRoot:     tempRoot,
	}

	result, err := runEdit(editOptions{
		Target: editTarget{Server: "prod", RemotePath: "/etc/app.conf"},
		Editor: "true",
	}, deps)
	require.ErrorIs(t, err, editorErr)
	assert.False(t, result.Changed)
	_, statErr := os.Stat(result.TempPath)
	assert.True(t, os.IsNotExist(statErr), "editor failure without changes should remove temp file")
}

func TestRunEditEditorFailureAfterChangePreservesTemp(t *testing.T) {
	tempRoot := t.TempDir()
	editorErr := errors.New("editor exited 1")
	deps := editDeps{
		download: func(target editTarget, localPath, workSessionID string) (ftpapi.DownloadedFile, error) {
			require.NoError(t, os.MkdirAll(filepath.Dir(localPath), 0700))
			require.NoError(t, os.WriteFile(localPath, []byte("original"), 0600))
			return ftpapi.DownloadedFile{Path: localPath, Size: int64(len("original"))}, nil
		},
		upload: func(target editTarget, localPath, workSessionID string) error {
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

	result, err := runEdit(editOptions{
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
	tempRoot := t.TempDir()
	var editorCalled bool
	deps := editDeps{
		download: func(target editTarget, localPath, workSessionID string) (ftpapi.DownloadedFile, error) {
			require.NoError(t, os.MkdirAll(filepath.Dir(localPath), 0700))
			require.NoError(t, os.WriteFile(localPath, []byte("large"), 0600))
			return ftpapi.DownloadedFile{Path: localPath, Size: maxEditPromptSize + 1}, nil
		},
		upload: func(target editTarget, localPath, workSessionID string) error {
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

	result, err := runEdit(editOptions{
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
