//go:build !windows

package selfupdate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAcquireLockRefusesToFollowASymlink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	require.NoError(t, os.WriteFile(victim, nil, 0600))
	path := filepath.Join(dir, ".alpacon-update.lock")
	require.NoError(t, os.Symlink(victim, path))

	_, err := AcquireLock(path)

	require.Error(t, err, "root is told to run this command, so a planted link must not decide what it opens")
}

func TestAcquireLockOpensALockFileThisUserCannotWrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root opens a file whatever its mode says")
	}
	path := filepath.Join(t.TempDir(), ".alpacon-update.lock")
	require.NoError(t, os.WriteFile(path, nil, 0444))

	lock, err := AcquireLock(path)
	require.NoError(t, err, "a lock file another account left behind must not end updates for everyone else")
	require.NoError(t, lock.Release())
}
