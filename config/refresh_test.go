package config

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alpacax/alpacon-cli/pkg/filelock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Serial: config helpers set HOME.
func TestRefreshRotationPersistsAtomicallyAndKeepsLogin(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://myws.us1.alpacon.io", "myws", "", "", "old-access", "old-refresh", "alpacon.io", 1, false))
	started := loadedConfig(t)
	// A config written before login IDs existed must migrate on its first refresh.
	started.Auth0LoginID = ""
	writeRefreshSnapshot(t, started)
	path := filepath.Join(os.Getenv("HOME"), ConfigFileDir, ConfigFileName)
	before, err := os.Stat(path)
	require.NoError(t, err)
	calls := 0
	grant := func(_ context.Context, token string) (RenewedToken, error) {
		calls++
		assert.Equal(t, "old-refresh", token)
		return RenewedToken{AccessToken: "new-access", RefreshToken: "new-refresh", ExpiresIn: 3600}, nil
	}
	result, err := RefreshAuth0Token(started, grant)
	require.NoError(t, err)
	assert.Equal(t, "new-refresh", result.RefreshToken)
	stored := loadedConfig(t)
	assert.Equal(t, "new-access", stored.AccessToken)
	assert.Equal(t, "new-refresh", stored.RefreshToken)
	assert.True(t, stored.sameLogin(started), "a refresh must retain the original login identity")
	require.NotEmpty(t, stored.Auth0LoginID)
	after, err := os.Stat(path)
	require.NoError(t, err)
	assert.False(t, os.SameFile(before, after), "refresh must atomically replace the config inode")
	if os.PathSeparator != '\\' {
		assert.Equal(t, os.FileMode(0600), after.Mode().Perm())
	}
	_, err = RefreshAuth0Token(started, grant)
	require.NoError(t, err)
	assert.Equal(t, 1, calls)
	require.NoError(t, SwitchWorkspace(started, "https://next.us1.alpacon.io", "next", false, nil))
	assert.Equal(t, "new-refresh", loadedConfig(t).RefreshToken)
	require.NoError(t, RestoreWorkspace("https://next.us1.alpacon.io", started))
	assert.Equal(t, "new-refresh", loadedConfig(t).RefreshToken)
	// Re-login by the same account must still invalidate the original snapshot.
	require.NoError(t, CreateConfig(started.WorkspaceURL, "myws", "", "", "new-access", "new-refresh", "alpacon.io", 3600, false))
	assert.ErrorIs(t, DeleteConfig(started), ErrLoginChanged)
}

// writeRefreshSnapshot models a writer that predates refresh locking. Production
// writers all use the lock and cannot replace the config during the exchange.
func writeRefreshSnapshot(t *testing.T, cfg Config) {
	t.Helper()
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	path := filepath.Join(os.Getenv("HOME"), ConfigFileDir, ConfigFileName)
	require.NoError(t, os.WriteFile(path+".replacement", data, 0600))
	require.NoError(t, os.Rename(path+".replacement", path))
	require.NoError(t, os.Chtimes(path, time.Now(), time.Now()))
}

// Serial: sets HOME and controls the in-flight grant with the fake clock.
func TestConcurrentRefreshesShareSuccessAndFailure(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "success"}, {name: "failure", err: assert.AnError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupTestConfig(t)
			synctest.Test(t, func(t *testing.T) {
				require.NoError(t, CreateConfig("https://myws.us1.alpacon.io", "myws", "", "", "old-access", "old-refresh", "alpacon.io", 1, false))
				started := loadedConfig(t)
				release := make(chan struct{})
				var calls atomic.Int32
				grant := func(context.Context, string) (RenewedToken, error) {
					calls.Add(1)
					<-release
					if tc.err != nil {
						return RenewedToken{}, tc.err
					}
					return RenewedToken{AccessToken: "new-access", RefreshToken: "new-refresh", ExpiresIn: 3600}, nil
				}
				var done sync.WaitGroup
				for range 12 {
					done.Go(func() {
						result, err := RefreshAuth0Token(started, grant)
						if tc.err != nil {
							assert.ErrorIs(t, err, tc.err)
						} else {
							if err != nil {
								t.Errorf("refresh failed: %v", err)
								return
							}
							assert.Equal(t, "new-access", result.AccessToken)
						}
					})
				}
				synctest.Wait()
				close(release)
				done.Wait()
				assert.Equal(t, int32(1), calls.Load())
			})
		})
	}
}

func TestRefreshAfterWorkspaceSwitchKeepsWorkspaceAccessToken(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://first.example.com", "first", "", "", "first-access", "old-refresh", "", 1, false))
	started := loadedConfig(t)
	require.NoError(t, SwitchWorkspace(started, "https://second.example.com", "second", false, &RenewedToken{AccessToken: "second-access", ExpiresIn: 3600}))
	result, err := RefreshAuth0Token(started, func(_ context.Context, sent string) (RenewedToken, error) {
		assert.Equal(t, "old-refresh", sent)
		return RenewedToken{AccessToken: "first-renewed", RefreshToken: "rotated-refresh", ExpiresIn: 3600}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, "first-renewed", result.AccessToken)
	saved := loadedConfig(t)
	assert.Equal(t, "https://second.example.com", saved.WorkspaceURL)
	assert.Equal(t, "second-access", saved.AccessToken)
	assert.Equal(t, "rotated-refresh", saved.RefreshToken)
}

func TestRefreshGrantHasDeadlineAndReleasesLock(t *testing.T) {
	setupTestConfig(t)
	synctest.Test(t, func(t *testing.T) {
		require.NoError(t, CreateConfig("https://myws.example.com", "myws", "", "", "old-access", "old-refresh", "", 1, false))
		started := loadedConfig(t)
		start := time.Now()
		_, err := RefreshAuth0Token(started, func(ctx context.Context, _ string) (RenewedToken, error) {
			<-ctx.Done()
			return RenewedToken{}, ctx.Err()
		})
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Equal(t, RefreshGrantTimeout, time.Since(start))
		require.NoError(t, withConfigLock(func() error { return nil }))
	})
}

func TestRefreshWaiterCoversLockWaitAndGrant(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://myws.example.com", "myws", "", "", "old-access", "old-refresh", "", 1, false))
	started := loadedConfig(t)
	held, err := filelock.Acquire(configLockPath(t), 0644)
	require.NoError(t, err)
	defer func() { _ = held.Release() }()
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		grant := func(context.Context, string) (RenewedToken, error) {
			calls.Add(1)
			time.Sleep(14 * time.Second)
			return RenewedToken{AccessToken: "new-access", RefreshToken: "new-refresh", ExpiresIn: 3600}, nil
		}
		var done sync.WaitGroup
		for range 2 {
			done.Go(func() {
				result, err := RefreshAuth0Token(started, grant)
				if err != nil {
					t.Errorf("refresh failed: %v", err)
					return
				}
				assert.Equal(t, "new-access", result.AccessToken)
			})
		}
		synctest.Wait()
		time.Sleep(29 * time.Second)
		require.NoError(t, held.Release())
		done.Wait()
		assert.Equal(t, int32(1), calls.Load())
	})
}
