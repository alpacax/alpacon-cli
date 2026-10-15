//go:build !windows

package filelock

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestAcquireAppliesPermToANewFile(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		perm os.FileMode
	}{
		{name: "owner read and write", perm: 0600},
		{name: "owner read only", perm: 0400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "held.lock")

			lock, err := Acquire(path, tc.perm)
			require.NoError(t, err)
			require.NoError(t, lock.Release())

			info, err := os.Stat(path)
			require.NoError(t, err)
			assert.Equal(t, tc.perm, info.Mode().Perm())
		})
	}
}

func TestAcquireLeavesAnExistingFilesModeAlone(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "held.lock")
	require.NoError(t, os.WriteFile(path, nil, 0600))
	require.NoError(t, os.Chmod(path, 0640))

	lock, err := Acquire(path, 0600)
	require.NoError(t, err)
	require.NoError(t, lock.Release())

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0640), info.Mode().Perm())
}

func TestAcquireRefusesToFollowASymlink(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		targetExists bool
	}{
		{name: "link to an existing file", targetExists: true},
		{name: "dangling link", targetExists: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			target := filepath.Join(dir, "target")
			if tc.targetExists {
				require.NoError(t, os.WriteFile(target, nil, 0600))
			}
			path := filepath.Join(dir, "held.lock")
			require.NoError(t, os.Symlink(target, path))

			lock, err := Acquire(path, 0600)

			require.ErrorIs(t, err, unix.ELOOP, "a planted link must not decide what a privileged caller opens")
			assert.Nil(t, lock)
			if !tc.targetExists {
				assert.NoFileExists(t, target, "a dangling link must not create the file it names")
			}
		})
	}
}

func TestAcquireOpensALockFileThisUserCannotWrite(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root opens a file whatever its mode says")
	}
	path := filepath.Join(t.TempDir(), "held.lock")
	require.NoError(t, os.WriteFile(path, nil, 0444))

	lock, err := Acquire(path, 0600)

	require.NoError(t, err, "flock needs only a read-only descriptor, so a file another account left behind still locks")
	require.NoError(t, lock.Release())
}
