package kubeconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const twoClusters = `apiVersion: v1
clusters:
- cluster:
    server: https://a.example.com
  name: alpacon-a
- cluster:
    server: https://b.example.com
  name: alpacon-b
contexts:
- context:
    cluster: alpacon-a
    user: alpacon-a
  name: alpacon-a
- context:
    cluster: alpacon-b
    user: alpacon-b
  name: alpacon-b
current-context: alpacon-a
kind: Config
users:
- name: alpacon-a
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1
      args:
      - kube
      - credentials
      - --cluster
      - a
      command: alpacon
      interactiveMode: Never
- name: alpacon-b
  user:
    token: static
`

func writeKubeconfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
	return path
}

func TestRemove(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		target      string
		want        Removal
		wantCurrent string
		kept        string
	}{
		{
			name:        "current context is cleared",
			target:      "alpacon-a",
			want:        Removal{Changed: true, ClearedCurrent: true},
			wantCurrent: "",
			kept:        "alpacon-b",
		},
		{
			name:        "another context keeps current and reports a static token",
			target:      "alpacon-b",
			want:        Removal{Changed: true, StaticToken: true},
			wantCurrent: "alpacon-a",
			kept:        "alpacon-a",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := writeKubeconfig(t, twoClusters)
			ed, err := Open(path)
			require.NoError(t, err)
			defer func() { _ = ed.Close() }()

			assert.Equal(t, tt.want, ed.Remove(tt.target))
			require.NoError(t, ed.Save())

			gone, err := Inspect(path, tt.target)
			require.NoError(t, err)
			assert.False(t, gone.Entry, "%s still held: %+v", tt.target, gone)
			kept, err := Inspect(path, tt.kept)
			require.NoError(t, err)
			assert.True(t, kept.Entry, "%s lost: %+v", tt.kept, kept)
			current, err := CurrentContext([]string{path})
			require.NoError(t, err)
			assert.Equal(t, tt.wantCurrent, current)
		})
	}
}

func TestRemoveRemovesEveryDuplicateAndReportsNoChangeOnAMiss(t *testing.T) {
	t.Parallel()
	path := writeKubeconfig(t, "clusters:\n- name: x\n  cluster: {server: 'https://1'}\n- name: x\n  cluster: {server: 'https://2'}\n")
	ed, err := Open(path)
	require.NoError(t, err)
	defer func() { _ = ed.Close() }()

	miss := ed.Remove("missing")
	assert.False(t, miss.Changed, "a miss must change nothing: %+v", miss)
	hit := ed.Remove("x")
	assert.True(t, hit.Changed, "the duplicates must go: %+v", hit)
	require.NoError(t, ed.Save())

	h, err := Inspect(path, "x")
	require.NoError(t, err)
	assert.False(t, h.Entry, "x still held: %+v", h)
}

func TestInspect(t *testing.T) {
	t.Parallel()
	path := writeKubeconfig(t, twoClusters)

	h, err := Inspect(path, "alpacon-a")
	require.NoError(t, err)
	assert.Equal(t, Holding{Entry: true, Current: true, Server: "https://a.example.com", PluginCluster: "a"}, h)

	static, err := Inspect(path, "alpacon-b")
	require.NoError(t, err)
	assert.Equal(t, Holding{Entry: true, Server: "https://b.example.com", StaticToken: true}, static)

	missing, err := Inspect(filepath.Join(t.TempDir(), "none"), "alpacon-a")
	require.NoError(t, err)
	assert.Equal(t, Holding{}, missing)

	_, err = Inspect(writeKubeconfig(t, "clusters: 1\n"), "alpacon-a")
	require.ErrorIs(t, err, ErrMalformed)
}

func TestCurrentContext(t *testing.T) {
	t.Parallel()
	first := writeKubeconfig(t, "current-context: \"\"\n")
	second := writeKubeconfig(t, "current-context: second\n")
	missing := filepath.Join(t.TempDir(), "none")

	got, err := CurrentContext([]string{missing, first, second})
	require.NoError(t, err)
	assert.Equal(t, "second", got)

	_, err = CurrentContext([]string{writeKubeconfig(t, "clusters: 1\n"), second})
	require.ErrorIs(t, err, ErrMalformed)
}

func TestPluginClusterNeedsTheCredentialsCommand(t *testing.T) {
	t.Parallel()
	path := writeKubeconfig(t, `users:
- name: other
  user:
    exec:
      args: [get-token, --cluster, a]
      command: aws
`)
	h, err := Inspect(path, "other")
	require.NoError(t, err)
	assert.Empty(t, h.PluginCluster)
}

// A context can name a cluster and user of other names: by hand, or after
// 'kubectl config rename-context'. Remove takes only what carries the name and
// reports what it left.
func TestRemoveReportsWhatItLeaves(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		want Removal
	}{
		{
			name: "user of another name keeps its token",
			body: "contexts:\n- context: {cluster: c1, user: u1}\n  name: alpacon-a\nusers:\n- name: u1\n  user:\n    token: secret\n",
			want: Removal{Changed: true, KeptUser: "u1", KeptUserToken: true},
		},
		{
			name: "renamed context still names the removed entries",
			body: "clusters:\n- cluster: {server: 'https://a'}\n  name: alpacon-a\ncontexts:\n- context: {cluster: alpacon-a, user: alpacon-a}\n  name: prod\ncurrent-context: prod\nusers:\n- name: alpacon-a\n  user: {token: x}\n",
			want: Removal{Changed: true, StaticToken: true, Dangling: []string{"prod"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ed, err := Open(writeKubeconfig(t, tt.body))
			require.NoError(t, err)
			defer func() { _ = ed.Close() }()

			assert.Equal(t, tt.want, ed.Remove("alpacon-a"))
		})
	}
}

// The server comes from the cluster the context names, which need not share
// its name; the logout picks the saved token by it.
func TestInspectFollowsTheContextsCluster(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		body       string
		wantServer string
	}{
		{name: "cluster of another name", body: "clusters:\n- cluster: {server: 'https://own'}\n  name: alpacon-a\n- cluster: {server: 'https://moved'}\n  name: c1\ncontexts:\n- context: {cluster: c1, user: alpacon-a}\n  name: alpacon-a\n", wantServer: "https://moved"},
		{name: "named cluster missing", body: "clusters:\n- cluster: {server: 'https://own'}\n  name: alpacon-a\ncontexts:\n- context: {cluster: c1, user: alpacon-a}\n  name: alpacon-a\n", wantServer: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h, err := Inspect(writeKubeconfig(t, tt.body), "alpacon-a")
			require.NoError(t, err)
			assert.Equal(t, tt.wantServer, h.Server)
		})
	}
}
