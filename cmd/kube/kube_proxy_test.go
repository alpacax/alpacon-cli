package kube

import (
	"path/filepath"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/filelock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProxyTarget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		server   string
		expected string
		wantErr  string
	}{
		{name: "default port", server: "https://my-workspace.us1.alpacon.io/kube/c-prod", expected: "my-workspace.us1.alpacon.io:443"},
		{name: "explicit port", server: "https://proxy.example.com:8443/kube/c-prod", expected: "proxy.example.com:8443"},
		{name: "IPv6 literal", server: "https://[2001:db8::1]/kube/c-prod", expected: "[2001:db8::1]:443"},
		{name: "plain http", server: "http://proxy.example.com/kube/c-prod", wantErr: "not https"},
		{name: "no host", server: "https:///kube/c-prod", wantErr: "not a URL with a host"},
		{name: "unparsable", server: "https://[::1", wantErr: "not a URL with a host"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			target, err := proxyTarget(tt.server)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, target)
		})
	}
}

func TestKubeProxyLockPath(t *testing.T) {
	t.Parallel()
	dir := filepath.Join("home", ".alpacon")

	path := kubeProxyLockPath(dir, "prod-east", 18443)

	assert.Equal(t, dir, filepath.Dir(path))
	assert.Regexp(t, `^\.kube_proxy_lock\.[0-9a-f]{16}\.18443$`, filepath.Base(path))
	assert.Equal(t, path, kubeProxyLockPath(dir, "prod-east", 18443))
	assert.NotEqual(t, path, kubeProxyLockPath(dir, "prod-east", 18444))
	assert.NotEqual(t, path, kubeProxyLockPath(dir, "prod-west", 18443))
	assert.Equal(t, dir, filepath.Dir(kubeProxyLockPath(dir, "../../etc/passwd", 1)), "a cluster name never leaves the directory")
}

func TestKubeProxyLockIsPerClusterAndPort(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	first, err := filelock.Acquire(kubeProxyLockPath(dir, "prod-east", 18443), 0644)
	require.NoError(t, err)

	_, err = filelock.Acquire(kubeProxyLockPath(dir, "prod-east", 18443), 0644)
	require.ErrorIs(t, err, filelock.ErrLocked)

	otherPort, err := filelock.Acquire(kubeProxyLockPath(dir, "prod-east", 18444), 0644)
	require.NoError(t, err)
	otherCluster, err := filelock.Acquire(kubeProxyLockPath(dir, "prod-west", 18443), 0644)
	require.NoError(t, err)

	require.NoError(t, first.Release())
	again, err := filelock.Acquire(kubeProxyLockPath(dir, "prod-east", 18443), 0644)
	require.NoError(t, err, "a released lock is free for the next proxy")

	for _, lock := range []*filelock.Lock{otherPort, otherCluster, again} {
		require.NoError(t, lock.Release())
	}
}
