package kube

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newGateTree builds a root with a kube look-alike so the gate can be driven
// in parallel: the package-level KubeCmd is not read at all, since Cobra
// mutates a command lazily. cmd/kube_gate_test.go covers the real tree.
func newGateTree() (root, real *cobra.Command) {
	root = &cobra.Command{Use: "alpacon", Run: func(*cobra.Command, []string) {}}
	real = &cobra.Command{Use: "kube", Aliases: []string{"k8s", "clusters"}, Short: "View Kubernetes clusters", Args: cobra.NoArgs}
	real.AddCommand(&cobra.Command{Use: "ls", Run: func(*cobra.Command, []string) {}})
	real.AddCommand(&cobra.Command{Use: "describe CLUSTER", Run: func(*cobra.Command, []string) {}})
	root.AddCommand(real)
	root.AddCommand(&cobra.Command{Use: "server", Short: "Manage registered servers", Run: func(*cobra.Command, []string) {}})
	return root, real
}

func TestApplySurfaceGate_EnabledKeepsCommand(t *testing.T) {
	t.Parallel()
	root, real := newGateTree()

	applySurfaceGate(root, real, true)

	found, _, err := root.Find([]string{"k8s", "ls"})
	require.NoError(t, err)
	assert.Equal(t, "ls", found.Name())
	assert.Same(t, real, found.Parent())
	assert.False(t, real.Hidden)
	assert.Contains(t, root.SuggestionsFor("kub"), "kube")
}

func TestApplySurfaceGate_DisabledSwapsInHiddenStub(t *testing.T) {
	t.Parallel()
	root, real := newGateTree()

	applySurfaceGate(root, real, false)

	for _, path := range [][]string{{"kube"}, {"k8s", "ls"}, {"clusters", "describe", "prod"}, {"kube", "--help"}} {
		found, _, err := root.Find(path)
		require.NoError(t, err, "%v", path)
		assert.NotSame(t, real, found, "%v", path)
		assert.Equal(t, "kube", found.Name(), "%v", path)
		assert.True(t, found.Hidden, "%v must resolve to the hidden stub", path)
		assert.True(t, found.DisableFlagParsing, "%v", path)
		assert.False(t, found.HasSubCommands(), "%v", path)
	}
	assert.Nil(t, real.Parent())
}

func TestApplySurfaceGate_DisabledRootHelpOmitsKube(t *testing.T) {
	t.Parallel()
	root, real := newGateTree()
	applySurfaceGate(root, real, false)

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--help"})
	require.NoError(t, root.Execute())

	assert.Contains(t, out.String(), "server")
	assert.NotRegexp(t, `(?m)^\s+kube\s`, out.String())
}

func TestApplySurfaceGate_DisabledOffersNoSuggestion(t *testing.T) {
	t.Parallel()
	root, real := newGateTree()
	applySurfaceGate(root, real, false)

	assert.Empty(t, root.SuggestionsFor("kub"))
}

func TestApplySurfaceGate_EnabledRootHelpListsKube(t *testing.T) {
	t.Parallel()
	root, real := newGateTree()
	applySurfaceGate(root, real, true)

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--help"})
	require.NoError(t, root.Execute())

	assert.Regexp(t, `(?m)^\s+kube\s`, out.String())
}
