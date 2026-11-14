package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

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
	grant := func(token string) (RenewedToken, error) {
		calls++
		assert.Equal(t, "old-refresh", token)
		return RenewedToken{AccessToken: "new-access", RefreshToken: "new-refresh", ExpiresIn: 3600}, nil
	}
	result, err := RefreshAuth0Token(started, grant, func(error) bool { return false })
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
	_, err = RefreshAuth0Token(started, grant, func(error) bool { return false })
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

// Serial: each case sets HOME; the fake clock pins the reuse boundary exactly.
func TestRefreshRecoversOnlyARecentRotation(t *testing.T) {
	cases := []struct {
		name                                                     string
		delay                                                    time.Duration
		newLogin, otherWorkspace, unchanged, expired, otherError bool
		recover                                                  bool
	}{
		{name: "rotation", recover: true},
		{name: "late rotation during slow grant", recover: true},
		{name: "at reuse boundary", delay: 30 * time.Second, recover: true},
		{name: "past reuse boundary", delay: 30*time.Second + time.Nanosecond},
		{name: "another login", newLogin: true},
		{name: "another workspace", otherWorkspace: true},
		{name: "unchanged token", unchanged: true},
		{name: "expired replacement", expired: true},
		{name: "another refusal", otherError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupTestConfig(t)
			synctest.Test(t, func(t *testing.T) {
				require.NoError(t, CreateConfig("https://myws.us1.alpacon.io", "myws", "", "", "old-access", "old-refresh", "alpacon.io", 1, false))
				started := loadedConfig(t)
				result, err := RefreshAuth0Token(started, func(string) (RenewedToken, error) {
					if tc.name == "late rotation during slow grant" {
						time.Sleep(31 * time.Second)
					}
					latest := started
					if !tc.unchanged {
						latest.RefreshToken = "new-refresh"
						latest.AccessToken = "new-access"
					}
					latest.AccessTokenExpiresAt = time.Now().Add(time.Hour).Format(time.RFC3339)
					if tc.newLogin {
						latest.Auth0LoginID = "another-login"
					}
					if tc.otherWorkspace {
						latest.WorkspaceURL = "https://next.us1.alpacon.io"
					}
					if tc.expired {
						latest.AccessTokenExpiresAt = time.Now().Format(time.RFC3339)
					}
					writeRefreshSnapshot(t, latest)
					time.Sleep(tc.delay)
					return RenewedToken{}, assert.AnError
				}, func(error) bool { return !tc.otherError })
				if tc.recover {
					require.NoError(t, err)
					assert.Equal(t, "new-access", result.AccessToken)
					assert.Equal(t, "new-refresh", result.RefreshToken)
				} else {
					require.ErrorIs(t, err, assert.AnError)
					assert.Empty(t, result.AccessToken)
				}
			})
		})
	}
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
				grant := func(string) (RenewedToken, error) {
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
						result, err := RefreshAuth0Token(started, grant, func(error) bool { return false })
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
