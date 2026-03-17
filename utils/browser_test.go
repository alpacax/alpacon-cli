package utils

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestShouldOpenBrowser(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		envSet   map[string]string
		expected bool
	}{
		{
			name:     "valid https URL",
			url:      "https://auth.alpacon.io/authorize?code=abc",
			expected: true,
		},
		{
			name:     "valid http URL",
			url:      "http://localhost:8080/callback",
			expected: true,
		},
		{
			name:     "non-http URL rejected",
			url:      "file:///etc/passwd",
			expected: false,
		},
		{
			name:     "empty URL rejected",
			url:      "",
			expected: false,
		},
		{
			name:     "ftp URL rejected",
			url:      "ftp://example.com/file",
			expected: false,
		},
		{
			name:     "SSH_CONNECTION blocks browser",
			url:      "https://auth.alpacon.io/authorize",
			envSet:   map[string]string{"SSH_CONNECTION": "1.2.3.4 1234 5.6.7.8 22"},
			expected: false,
		},
		{
			name:     "SSH_TTY blocks browser",
			url:      "https://auth.alpacon.io/authorize",
			envSet:   map[string]string{"SSH_TTY": "/dev/pts/0"},
			expected: false,
		},
		{
			name:     "ALPACON_NO_BROWSER=1 blocks browser",
			url:      "https://auth.alpacon.io/authorize",
			envSet:   map[string]string{"ALPACON_NO_BROWSER": "1"},
			expected: false,
		},
		{
			name:     "ALPACON_NO_BROWSER=true blocks browser",
			url:      "https://auth.alpacon.io/authorize",
			envSet:   map[string]string{"ALPACON_NO_BROWSER": "true"},
			expected: false,
		},
		{
			name:     "ALPACON_NO_BROWSER=0 does not block",
			url:      "https://auth.alpacon.io/authorize",
			envSet:   map[string]string{"ALPACON_NO_BROWSER": "0"},
			expected: true,
		},
		{
			name:     "ALPACON_NO_BROWSER=false does not block",
			url:      "https://auth.alpacon.io/authorize",
			envSet:   map[string]string{"ALPACON_NO_BROWSER": "false"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// t.Setenv registers cleanup automatically; setting then
			// unsetting ensures the var is absent for the test body.
			for _, key := range []string{"SSH_CONNECTION", "SSH_TTY", "ALPACON_NO_BROWSER"} {
				t.Setenv(key, "")
				_ = os.Unsetenv(key)
			}

			for k, v := range tt.envSet {
				t.Setenv(k, v)
			}

			assert.Equal(t, tt.expected, shouldOpenBrowser(tt.url))
		})
	}
}

func TestAcquireBrowserLock(t *testing.T) {
	// Use a temp dir as home to isolate the lock file
	tmpDir := t.TempDir()
	alpaconDir := filepath.Join(tmpDir, ".alpacon")
	lockFile := filepath.Join(alpaconDir, ".browser_lock")

	// Patch browserLockPath for this test
	origFunc := browserLockPathFunc
	browserLockPathFunc = func() string { return lockFile }
	t.Cleanup(func() { browserLockPathFunc = origFunc })

	// First call should succeed
	assert.True(t, acquireBrowserLock(), "first acquire should succeed")

	// Lock file should exist
	_, err := os.Stat(lockFile)
	assert.NoError(t, err, "lock file should exist after acquire")

	// Second call within debounce window should be blocked
	assert.False(t, acquireBrowserLock(), "second acquire within debounce should be blocked")

	// Backdate the lock file to simulate expiry
	expired := time.Now().Add(-browserDebounce - time.Second)
	assert.NoError(t, os.Chtimes(lockFile, expired, expired))

	// Now acquire should succeed again
	assert.True(t, acquireBrowserLock(), "acquire after debounce expiry should succeed")
}
