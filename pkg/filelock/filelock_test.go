package filelock

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAcquireRefusesASecondHolder(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "held.lock")

	first, err := Acquire(path, 0600)
	require.NoError(t, err)
	defer func() { _ = first.Release() }()

	second, err := Acquire(path, 0600)

	require.ErrorIs(t, err, ErrLocked)
	assert.Nil(t, second)
}

func TestReleaseLetsTheNextHolderIn(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "held.lock")
	first, err := Acquire(path, 0600)
	require.NoError(t, err)

	require.NoError(t, first.Release())

	next, err := Acquire(path, 0600)
	require.NoError(t, err)
	require.NoError(t, next.Release())
	assert.FileExists(t, path, "the file stays behind, so a later holder locks the same file an earlier one did")
}

func TestAcquireTakesALockFileNoProcessHolds(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "held.lock")
	require.NoError(t, os.WriteFile(path, []byte("4242"), 0600))

	lock, err := Acquire(path, 0600)

	require.NoError(t, err, "a file left behind by a process that died must not block the next holder")
	require.NoError(t, lock.Release())
}

func TestAcquireGivesAnUnheldLockToExactlyOneClaimant(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "held.lock")

	const claimants = 16
	var start sync.WaitGroup
	var done sync.WaitGroup
	var mu sync.Mutex
	var locks []*Lock
	var refusals []error
	start.Add(1)
	for range claimants {
		done.Go(func() {
			start.Wait()
			lock, err := Acquire(path, 0600)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				refusals = append(refusals, err)
				return
			}
			locks = append(locks, lock)
		})
	}
	start.Done()
	done.Wait()
	defer func() {
		for _, lock := range locks {
			_ = lock.Release()
		}
	}()

	assert.Len(t, locks, 1, "claimants that reach an unheld lock at once must leave exactly one holder")
	for _, err := range refusals {
		assert.ErrorIs(t, err, ErrLocked)
	}
}

func TestAcquireCreatesTheMissingDirectory(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nested", "dir", "held.lock")

	lock, err := Acquire(path, 0600)

	require.NoError(t, err)
	require.NoError(t, lock.Release())
	assert.FileExists(t, path)
}

func TestAcquireSurfacesADirectoryItCannotCreate(t *testing.T) {
	t.Parallel()
	blocked := filepath.Join(t.TempDir(), "bin")
	require.NoError(t, os.WriteFile(blocked, []byte("not a directory"), 0600))

	lock, err := Acquire(filepath.Join(blocked, "held.lock"), 0600)

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrLocked, "a lock that cannot be made is not one someone else holds")
	assert.Nil(t, lock)
}
