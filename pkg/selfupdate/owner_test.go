package selfupdate

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPackageOwner(t *testing.T) {
	t.Parallel()
	notFound := errors.New("exit status 1")

	tests := []struct {
		name string
		run  CommandRunner
		want string
	}{
		{
			name: "dpkg claims the file",
			run: func(name string, args ...string) ([]byte, error) {
				if name == "dpkg" {
					return []byte("alpacon: /usr/local/bin/alpacon\n"), nil
				}
				return nil, notFound
			},
			want: "deb:alpacon",
		},
		{
			name: "rpm claims the file",
			run: func(name string, args ...string) ([]byte, error) {
				if name == "rpm" {
					return []byte("alpacon-1.4.0-1.x86_64\n"), nil
				}
				return nil, notFound
			},
			want: "rpm:alpacon-1.4.0-1.x86_64",
		},
		{
			name: "no tool claims the file",
			run: func(name string, args ...string) ([]byte, error) {
				return nil, notFound
			},
			want: "",
		},
		{
			name: "dpkg exits zero with a line carrying no colon",
			run: func(name string, args ...string) ([]byte, error) {
				if name == "dpkg" {
					return []byte("no path found matching pattern\n"), nil
				}
				return nil, notFound
			},
			want: "",
		},
		{
			name: "a tool exits zero with no output",
			run: func(name string, args ...string) ([]byte, error) {
				return []byte("   \n"), nil
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, err := PackageOwner(tt.run, "/usr/local/bin/alpacon")

			require.NoError(t, err)
			assert.Equal(t, tt.want, owner)
		})
	}
}

// No querier is the strongest form of "the query could not answer", and
// answering "" would have ClassifyPath call it a manual install.
func TestPackageOwnerRefusesAnAnswerWithNoWayToAsk(t *testing.T) {
	t.Parallel()
	owner, err := PackageOwner(nil, "/usr/local/bin/alpacon")

	require.ErrorIs(t, err, ErrOwnerUnknown)
	assert.Empty(t, owner)
}

// A query that could not answer must not read as "nobody owns it": ClassifyPath
// turns that into a manual install, which is permission to overwrite the file.
func TestPackageOwnerRefusesToGuessWhenTheQueryCannotAnswer(t *testing.T) {
	t.Parallel()
	for _, stalled := range []string{"dpkg", "rpm"} {
		t.Run(stalled, func(t *testing.T) {
			run := func(name string, args ...string) ([]byte, error) {
				if name == stalled {
					return nil, fmt.Errorf("%w: %s: signal: killed", ErrOwnerUnknown, name)
				}
				return nil, errors.New("exit status 1")
			}

			owner, err := PackageOwner(run, "/usr/local/bin/alpacon")

			require.ErrorIs(t, err, ErrOwnerUnknown)
			assert.Empty(t, owner)

			_, kindErr := DetectInstallKind(run, "/usr/local/bin/alpacon")
			assert.ErrorIs(t, kindErr, ErrOwnerUnknown)
		})
	}
}

// $PATH must never name the manager: 'sudo alpacon update' carries the
// operator's PATH wherever sudoers has no secure_path.
func TestResolvePackageManagerNeverLeavesTheNameToThePath(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"dpkg", "rpm"} {
		path, installed := resolvePackageManager(name)
		if installed {
			assert.True(t, strings.HasPrefix(path, "/"), "%s must resolve to an absolute path, got %q", name, path)
		} else {
			assert.Empty(t, path)
		}
	}

	path, installed := resolvePackageManager("/bin/sh")
	assert.True(t, installed, "a name this map does not pin is passed through for the tests that use one")
	assert.Equal(t, "/bin/sh", path)
}
