package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/alpacax/alpacon-cli/pkg/filelock"
)

const (
	lockFileName      = ConfigFileName + ".lock" // saveConfig's rename replaces config.json, so a lock on it would exclude nobody
	lockTimeout       = 10 * time.Second
	lockRetryInterval = 20 * time.Millisecond
)

// withConfigLock runs fn under the lock every config writer takes, giving up after lockTimeout.
// Not reentrant: fn calling another writer times out on the lock it already holds.
func withConfigLock(fn func() error) error {
	lock, err := acquireConfigLock()
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()
	return fn()
}

func acquireConfigLock() (*filelock.Lock, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	lockPath := filepath.Join(dir, lockFileName)

	deadline := time.Now().Add(lockTimeout)
	for {
		lock, err := filelock.Acquire(lockPath, 0644) // stays empty; the 0700 directory keeps other accounts out
		switch {
		case err == nil:
			return lock, nil
		case !errors.Is(err, filelock.ErrLocked):
			return nil, fmt.Errorf("failed to lock %s: %v", lockPath, err)
		case !time.Now().Before(deadline):
			return nil, fmt.Errorf("timed out after %s waiting for another alpacon process to release %s", lockTimeout, lockPath)
		}
		time.Sleep(lockRetryInterval)
	}
}

// updateConfig loads the config under withConfigLock, lets apply change it and
// saves the result. apply reports false to leave the file untouched.
func updateConfig(apply func(cfg *Config) (bool, error)) error {
	return withConfigLock(func() error {
		cfg, err := LoadConfig()
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		changed, err := apply(&cfg)
		if err != nil || !changed {
			return err
		}
		return saveConfig(&cfg)
	})
}
