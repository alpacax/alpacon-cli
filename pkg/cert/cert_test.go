package cert

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generateKey now saves through utils.SaveStreamAtomic (#439); this pins the
// permissions and round-trip that migration must preserve.
func TestGenerateKey_WritesOwnerOnlyKeyFile(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Unix file modes are not enforced on Windows")
	}

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "nested", "server.key")

	key, err := generateKey(keyPath)
	require.NoError(t, err)
	require.NotNil(t, key)

	info, err := os.Stat(keyPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())

	dirInfo, err := os.Stat(filepath.Dir(keyPath))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0700), dirInfo.Mode().Perm())

	reread, err := readPrivateKey(keyPath)
	require.NoError(t, err)
	assert.Equal(t, key.N, reread.N)
}

// generateKey's own cache: a second call must reuse the key on disk rather
// than overwrite it with a fresh one.
func TestGenerateKey_ReusesExistingKey(t *testing.T) {
	t.Parallel()

	keyPath := filepath.Join(t.TempDir(), "server.key")

	first, err := generateKey(keyPath)
	require.NoError(t, err)

	second, err := generateKey(keyPath)
	require.NoError(t, err)

	assert.Equal(t, first.N, second.N)
}
