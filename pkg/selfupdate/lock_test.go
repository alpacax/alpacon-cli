package selfupdate

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAcquireLockRefusesASecondHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update.lock")

	first, err := AcquireLock(path)
	require.NoError(t, err)

	_, err = AcquireLock(path)
	assert.ErrorIs(t, err, ErrUpdateInProgress)

	require.NoError(t, first.Release())

	second, err := AcquireLock(path)
	require.NoError(t, err)
	require.NoError(t, second.Release())
}

func TestAcquireLockTakesALockFileNoProcessHolds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update.lock")
	require.NoError(t, os.WriteFile(path, []byte("4242"), 0600))

	lock, err := AcquireLock(path)

	require.NoError(t, err, "a lock file left behind by a process that died must not block the next update")
	require.NoError(t, lock.Release())
}

func TestReleaseLetsTheNextHolderIn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update.lock")
	lock, err := AcquireLock(path)
	require.NoError(t, err)

	require.NoError(t, lock.Release())

	next, err := AcquireLock(path)
	require.NoError(t, err)
	require.NoError(t, next.Release())
}

func TestAcquireLockGivesAnUnheldLockToExactlyOneClaimant(t *testing.T) {
	path := filepath.Join(t.TempDir(), "update.lock")
	require.NoError(t, os.WriteFile(path, []byte("4242"), 0600))

	const claimants = 16
	var start sync.WaitGroup
	var done sync.WaitGroup
	var held sync.Mutex
	var locks []*Lock
	start.Add(1)
	for range claimants {
		done.Go(func() {
			start.Wait()
			lock, err := AcquireLock(path)
			if err != nil {
				return
			}
			held.Lock()
			locks = append(locks, lock)
			held.Unlock()
		})
	}
	start.Done()
	done.Wait()

	assert.Len(t, locks, 1, "an unheld lock file that several processes reach at once must go to exactly one")
	for _, lock := range locks {
		_ = lock.Release()
	}
}

func TestLockPathSitsBesideTheBinaryItGuards(t *testing.T) {
	assert.Equal(t, filepath.Join("/usr/local/bin", ".alpacon-update.lock"), LockPath("/usr/local/bin/alpacon"))
}

func TestAcquireLockNamesItselfWhicheverWayTheLockCannotBeMade(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "bin")
	require.NoError(t, os.WriteFile(blocked, []byte("not a directory"), 0600))

	_, err := AcquireLock(filepath.Join(blocked, ".alpacon-update.lock"))

	assert.ErrorIs(t, err, ErrLockUnavailable)
}
