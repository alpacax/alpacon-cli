package selfupdate

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPackageOwner(t *testing.T) {
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
			assert.Equal(t, tt.want, PackageOwner(tt.run, "/usr/local/bin/alpacon"))
		})
	}
}

func TestPackageOwnerTreatsANilRunnerAsNoOwner(t *testing.T) {
	assert.Empty(t, PackageOwner(nil, "/usr/local/bin/alpacon"), "a caller with no way to ask has a manual install")
}
