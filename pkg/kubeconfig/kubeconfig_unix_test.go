//go:build unix

package kubeconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveCreatesOwnerOnlyFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "a", ".kube", "config")

	applyAndSave(t, path, testEntry())

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func TestSaveWritesThroughSymlink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "kubeconfig")
	writeFile(t, target, kubectlFixture)
	link := filepath.Join(dir, ".kube", "config")
	require.NoError(t, os.MkdirAll(filepath.Dir(link), 0755))
	require.NoError(t, os.Symlink(target, link))

	applyAndSave(t, link, testEntry())

	info, err := os.Lstat(link)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSymlink, "the link must survive the write")
	server, _, err := Lookup(target, testName)
	require.NoError(t, err)
	assert.Equal(t, testServer, server)
	assert.NoFileExists(t, link+".lock")
}

// The lock directory is writable but the symlink's target directory is not, so
// only the check on the directory Save will write to can catch it.
func TestOpenRefusesUnwritableTargetDirectory(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root writes to a read-only directory")
	}
	dir := t.TempDir()
	targetDir := filepath.Join(dir, "readonly")
	target := filepath.Join(targetDir, "kubeconfig")
	writeFile(t, target, kubectlFixture)
	require.NoError(t, os.Chmod(targetDir, 0555))
	t.Cleanup(func() { _ = os.Chmod(targetDir, 0755) })
	link := filepath.Join(dir, "config")
	require.NoError(t, os.Symlink(target, link))

	ed, err := Open(link)

	require.Error(t, err)
	assert.Nil(t, ed)
	assert.Contains(t, err.Error(), "cannot write to "+targetDir)
	assert.NoFileExists(t, link+".lock")
	assert.Equal(t, kubectlFixture, readFile(t, target))
}

// Serial: it captures os.Stderr.
func TestSaveKeepsExistingModeAndWarnsWhenReadableByOthers(t *testing.T) {
	cases := []struct {
		name     string
		mode     os.FileMode
		wantWarn bool
	}{
		{name: "world readable", mode: 0644, wantWarn: true},
		{name: "group readable", mode: 0640, wantWarn: true},
		{name: "owner only", mode: 0600, wantWarn: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config")
			writeFile(t, path, kubectlFixture)
			require.NoError(t, os.Chmod(path, tc.mode))

			_, stderr := testutil.CaptureOutput(t, func() {
				applyAndSave(t, path, testEntry())
			})

			info, err := os.Stat(path)
			require.NoError(t, err)
			assert.Equal(t, tc.mode, info.Mode().Perm())
			// The generic advice to remove the file would cost the user their
			// other clusters, so it must not appear for a kubeconfig.
			assert.NotContains(t, stderr, "Remove it first")
			warning := path + " holds a bearer token and is readable by other accounts; run 'chmod 600 " + path + "'"
			if tc.wantWarn {
				assert.Contains(t, stderr, warning)
			} else {
				assert.Empty(t, stderr)
			}
		})
	}
}
