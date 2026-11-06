package kube

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreRemoveKeepsTheLockAndOtherRecords(t *testing.T) {
	t.Parallel()
	store := kubeSessionStore{dir: t.TempDir()}
	gone := kubeSessionRecord{Cluster: "a", WorkspaceURL: "https://ws.example.com"}
	kept := kubeSessionRecord{Cluster: "b", WorkspaceURL: "https://ws.example.com"}
	require.NoError(t, store.write(gone))
	require.NoError(t, store.write(kept))
	lock, err := store.lock(gone.key())
	require.NoError(t, err)
	require.NoError(t, lock.Release())
	staging := filepath.Join(store.dir, recordBase(gone.key())+".123.tmp")
	require.NoError(t, os.WriteFile(staging, []byte("token"), storeFilePerm))

	require.NoError(t, store.remove(gone.key()))
	require.NoError(t, store.remove(gone.key()), "a record already gone is not an error")
	require.NoError(t, store.removeStaging(gone.key()))

	assert.NoFileExists(t, store.recordPath(gone.key()))
	assert.NoFileExists(t, staging)
	assert.FileExists(t, filepath.Join(store.dir, recordBase(gone.key())+".lock"))
	assert.FileExists(t, store.recordPath(kept.key()))
}

// Glob reads '[' in the directory as pattern syntax and would find nothing.
func TestStoreFindsRecordsUnderAPatternCharacter(t *testing.T) {
	t.Parallel()
	store := kubeSessionStore{dir: filepath.Join(t.TempDir(), "me[1]")}
	record := kubeSessionRecord{Cluster: "a", WorkspaceURL: "https://ws.example.com"}
	require.NoError(t, store.write(record))
	staging := filepath.Join(store.dir, recordBase(record.key())+".123.tmp")
	require.NoError(t, os.WriteFile(staging, []byte("token"), storeFilePerm))

	found, err := store.records("a")
	require.NoError(t, err)
	assert.Len(t, found, 1)
	require.NoError(t, store.removeStaging(record.key()))
	assert.NoFileExists(t, staging)
}
