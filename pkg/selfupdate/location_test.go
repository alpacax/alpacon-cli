package selfupdate

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		executablePath string
		packageOwner   string
		want           InstallKind
	}{
		{name: "homebrew cellar", executablePath: "/opt/homebrew/Cellar/alpacon-cli/1.4.0/bin/alpacon", packageOwner: "", want: InstallHomebrew},
		{name: "intel homebrew cellar", executablePath: "/usr/local/Cellar/alpacon-cli/1.4.0/bin/alpacon", packageOwner: "", want: InstallHomebrew},
		{name: "mise shim", executablePath: "/home/dev/.local/share/mise/installs/alpacon/1.4.0/alpacon", packageOwner: "", want: InstallVersionManager},
		{name: "asdf install", executablePath: "/home/dev/.asdf/installs/alpacon/1.4.0/bin/alpacon", packageOwner: "", want: InstallVersionManager},
		{name: "owned by a deb package", executablePath: "/usr/local/bin/alpacon", packageOwner: "deb:alpacon", want: InstallDeb},
		{name: "owned by an rpm package", executablePath: "/usr/local/bin/alpacon", packageOwner: "rpm:alpacon", want: InstallRPM},
		{name: "same path with no owner is manual", executablePath: "/usr/local/bin/alpacon", packageOwner: "", want: InstallManual},
		{name: "home directory install is manual", executablePath: "/home/dev/.local/bin/alpacon", packageOwner: "", want: InstallManual},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ClassifyPath(tt.executablePath, tt.packageOwner))
		})
	}
}

func TestResolveExecutablePathFollowsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on some Windows environments")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "real-alpacon")
	require.NoError(t, os.WriteFile(target, []byte("binary"), 0755))
	link := filepath.Join(dir, "alpacon")
	require.NoError(t, os.Symlink(target, link))

	original := osExecutable
	t.Cleanup(func() { osExecutable = original })
	osExecutable = func() (string, error) { return link, nil }

	resolved, err := ResolveExecutablePath()

	require.NoError(t, err)
	assert.Equal(t, mustEvalSymlinks(t, target), resolved)
}

func mustEvalSymlinks(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	require.NoError(t, err)
	return resolved
}
