package kubeconfig

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Serial: it sets KUBECONFIG and the home variables, which are process-wide.
func TestResolvePath(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	require.NoError(t, os.MkdirAll(home, 0755))
	existing := filepath.Join(dir, "existing")
	writeFile(t, existing, "")
	missing1 := filepath.Join(dir, "missing1")
	missing2 := filepath.Join(dir, "missing2")
	list := func(paths ...string) string { return strings.Join(paths, string(os.PathListSeparator)) }
	sep := string(os.PathListSeparator)

	cases := []struct {
		name         string
		flag         string
		env          string
		want         string
		wantShadowed bool
	}{
		{name: "flag wins over KUBECONFIG", flag: missing1, env: list(existing, missing2), want: missing1},
		{name: "single missing entry", env: missing1, want: missing1},
		{name: "first existing entry", env: list(missing1, existing, missing2), want: existing, wantShadowed: true},
		{name: "first entry existing is not shadowed", env: list(existing, missing1), want: existing},
		{name: "none existing takes the last", env: list(missing1, missing2), want: missing2, wantShadowed: true},
		{name: "empty entries dropped", env: sep + missing1 + sep + sep, want: missing1},
		{name: "duplicates count once", env: list(missing1, missing1), want: missing1},
		{name: "only separators falls back to home", env: sep + sep, want: filepath.Join(home, ".kube", "config")},
		{name: "unset falls back to home", env: "", want: filepath.Join(home, ".kube", "config")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KUBECONFIG", tc.env)
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("HOMEDRIVE", "")
			t.Setenv("HOMEPATH", "")

			got, err := ResolvePath(tc.flag)

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.wantShadowed, Shadowed(got))
		})
	}
}

// SearchPaths lists what kubectl merges, and ReadByDefault answers whether a
// file is among them without --kubeconfig. Serial: it sets KUBECONFIG and HOME.
func TestSearchPathsAndReadByDefault(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	first, second, other := filepath.Join(dir, "first"), filepath.Join(dir, "second"), filepath.Join(dir, "other")
	defaultFile := filepath.Join(home, ".kube", "config")
	sep := string(os.PathListSeparator)

	cases := []struct {
		name       string
		flag       string
		env        string
		want       []string
		readByDflt map[string]bool
	}{
		{name: "flag alone", flag: other, env: first, want: []string{other}, readByDflt: map[string]bool{other: false, first: true}},
		{name: "every KUBECONFIG entry", env: first + sep + second + sep + first, want: []string{first, second},
			readByDflt: map[string]bool{first: true, second: true, other: false, defaultFile: false}},
		{name: "home without KUBECONFIG", want: []string{defaultFile}, readByDflt: map[string]bool{defaultFile: true, other: false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KUBECONFIG", tc.env)
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)

			got, err := SearchPaths(tc.flag)

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			for path, want := range tc.readByDflt {
				assert.Equal(t, want, ReadByDefault(path), path)
			}
		})
	}
}

// kubectl reads no file at all when KUBECONFIG holds only separators; it does
// not fall back to the home file. Serial: it sets KUBECONFIG and HOME.
func TestSearchPathsRefusesAKubeconfigNamingNoFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	sep := string(os.PathListSeparator)
	t.Setenv("KUBECONFIG", sep+sep)

	got, err := SearchPaths("")

	require.Error(t, err)
	assert.Empty(t, got)
	assert.False(t, ReadByDefault(filepath.Join(home, ".kube", "config")))
}

// A relative spelling names the same file. Serial: it changes the working
// directory and sets KUBECONFIG.
func TestReadByDefaultComparesRelativePaths(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("KUBECONFIG", filepath.Join(dir, "kc"))

	assert.True(t, ReadByDefault(filepath.Join(".", "sub", "..", "kc")), "a relative path to the same file")
	assert.False(t, ReadByDefault("other"))
}

// Without a home, kubectl would fall back to a .kube/config relative to the
// working directory; this refuses instead. Serial: it clears the home variables.
func TestResolvePathWithoutHomeIsAnError(t *testing.T) {
	for _, key := range []string{"KUBECONFIG", "HOME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH"} {
		t.Setenv(key, "")
	}

	got, err := ResolvePath("")

	require.Error(t, err)
	assert.Empty(t, got)
}

func TestWindowsHomeDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mkdir := func(name string, withConfig bool) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(p, 0755))
		if withConfig {
			writeFile(t, filepath.Join(p, ".kube", "config"), "")
		}
		return p
	}
	homeWithConfig := mkdir("home-config", true)
	profileWithConfig := mkdir("profile-config", true)
	plainHome := mkdir("home-plain", false)
	plainProfile := mkdir("profile-plain", false)
	drive := mkdir("drive", false)
	mkdir(filepath.Join("drive", "hp-config"), true)
	mkdir(filepath.Join("drive", "hp-plain"), false)
	notADir := filepath.Join(dir, "file")
	writeFile(t, notADir, "")
	absent := filepath.Join(dir, "absent")
	sep := string(filepath.Separator)

	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "HOME with config wins",
			env:  map[string]string{"HOME": homeWithConfig, "USERPROFILE": profileWithConfig},
			want: homeWithConfig,
		},
		{
			name: "HOMEDRIVE HOMEPATH with config precedes USERPROFILE with config",
			env:  map[string]string{"HOME": plainHome, "HOMEDRIVE": drive, "HOMEPATH": sep + "hp-config", "USERPROFILE": profileWithConfig},
			want: drive + sep + "hp-config",
		},
		{
			name: "USERPROFILE with config beats HOME without",
			env:  map[string]string{"HOME": plainHome, "USERPROFILE": profileWithConfig},
			want: profileWithConfig,
		},
		{
			name: "without config the first writable of HOME USERPROFILE HOMEDRIVE",
			env:  map[string]string{"HOME": absent, "HOMEDRIVE": drive, "HOMEPATH": sep + "hp-plain", "USERPROFILE": plainProfile},
			want: plainProfile,
		},
		{
			name: "HOMEDRIVE needs HOMEPATH",
			env:  map[string]string{"HOMEDRIVE": drive},
			want: "",
		},
		{
			name: "first existing when none is a directory",
			env:  map[string]string{"HOME": absent, "USERPROFILE": notADir},
			want: notADir,
		},
		{
			name: "first set when none exists",
			env:  map[string]string{"HOME": absent, "USERPROFILE": filepath.Join(dir, "absent2")},
			want: absent,
		},
		{
			name: "nothing set",
			env:  map[string]string{},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := windowsHomeDir(func(key string) string { return tc.env[key] })
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestWindowsHomeDirSkipsReadOnlyDirectory(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows reports no write bit for a directory")
	}
	dir := t.TempDir()
	readOnly := filepath.Join(dir, "readonly")
	writable := filepath.Join(dir, "writable")
	require.NoError(t, os.MkdirAll(readOnly, 0555))
	require.NoError(t, os.MkdirAll(writable, 0755))
	env := map[string]string{"HOME": readOnly, "USERPROFILE": writable}

	got := windowsHomeDir(func(key string) string { return env[key] })

	assert.Equal(t, writable, got)
}
