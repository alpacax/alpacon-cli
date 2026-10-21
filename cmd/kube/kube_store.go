package kube

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/filelock"
)

// kubeSessionRecord is what 'kube login' keeps of the kube session it opened
// for a cluster. The channel token comes back only when the session is
// created, so this file is the one place the credential plugin can read it
// from; the rest names the session and the workspace it belongs to.
type kubeSessionRecord struct {
	Cluster        string    `json:"cluster"`
	ClusterID      string    `json:"cluster_id"`
	WorkspaceName  string    `json:"workspace_name"`
	WorkspaceURL   string    `json:"workspace_url"`
	KubeSessionID  string    `json:"kube_session_id"`
	WorkSessionID  string    `json:"work_session_id"`
	ChannelToken   string    `json:"channel_token"`
	TokenExpiredAt time.Time `json:"token_expired_at"`
	ProxyURL       string    `json:"proxy_url"`
	SavedAt        time.Time `json:"saved_at"`
}

// sameWorkspace reports whether the record was written for the workspace a
// client was built for, the only one whose credentials can act on it.
func (r *kubeSessionRecord) sameWorkspace(workspaceName, workspaceURL string) bool {
	return r.WorkspaceName == workspaceName && r.WorkspaceURL == workspaceURL
}

// kubeSessionStore keeps one record per cluster name, the same key the
// kubeconfig context alpacon-CLUSTER uses, under a directory only the owner
// can enter.
type kubeSessionStore struct {
	dir string
}

const (
	storeDirPerm  = 0700
	storeFilePerm = 0600 // the record holds a bearer token
)

func defaultKubeSessionStore() (kubeSessionStore, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return kubeSessionStore{}, err
	}
	return kubeSessionStore{dir: filepath.Join(home, config.ConfigFileDir, config.KubeSessionDir)}, nil
}

// recordBase names a cluster's files. The name is hashed so any cluster name
// makes a safe file name.
func recordBase(clusterName string) string {
	sum := sha256.Sum256([]byte(clusterName))
	return hex.EncodeToString(sum[:])[:16]
}

func (s kubeSessionStore) recordPath(clusterName string) string {
	return filepath.Join(s.dir, recordBase(clusterName)+".json")
}

var errStoreLocked = errors.New("another 'alpacon kube login' for this cluster is running")

// lock serializes the logins of one cluster, whichever kubeconfig each one
// writes: each reads the record, replaces it and closes the session it named.
func (s kubeSessionStore) lock(clusterName string) (*filelock.Lock, error) {
	if err := s.ensureDir(); err != nil {
		return nil, err
	}
	lock, err := filelock.Acquire(filepath.Join(s.dir, recordBase(clusterName)+".lock"), storeFilePerm)
	if errors.Is(err, filelock.ErrLocked) {
		return nil, errStoreLocked
	}
	return lock, err
}

// read returns the cluster's record, or nil when there is none.
func (s kubeSessionStore) read(clusterName string) (*kubeSessionRecord, error) {
	data, err := os.ReadFile(s.recordPath(clusterName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record kubeSessionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", s.recordPath(clusterName), err)
	}
	// Two names sharing a hash prefix would share a file; neither may take the
	// other's record for its own.
	if record.Cluster != clusterName {
		return nil, nil
	}
	return &record, nil
}

// write replaces the cluster's record through a temp file renamed into place,
// so a reader never sees half a record. The rename replaces a symbolic link
// at the path rather than writing through it, and the file is owner-only
// whatever was there before.
func (s kubeSessionStore) write(record kubeSessionRecord) error {
	if err := s.ensureDir(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	path := s.recordPath(record.Cluster)
	tmp, err := os.CreateTemp(s.dir, recordBase(record.Cluster)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	if err := tmp.Chmod(storeFilePerm); err != nil && runtime.GOOS != "windows" {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// restore puts back the record a failed login replaced, or removes the new
// one when there was none before.
func (s kubeSessionStore) restore(clusterName string, previous *kubeSessionRecord) error {
	if previous != nil {
		return s.write(*previous)
	}
	err := os.Remove(s.recordPath(clusterName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// ensureDir creates the store's directory owner-only and narrows one that
// already grants other accounts access. Windows has no Unix modes to narrow.
func (s kubeSessionStore) ensureDir() error {
	if err := os.MkdirAll(s.dir, storeDirPerm); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	info, err := os.Lstat(s.dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", s.dir)
	}
	if info.Mode().Perm()&0077 != 0 {
		return os.Chmod(s.dir, storeDirPerm)
	}
	return nil
}
