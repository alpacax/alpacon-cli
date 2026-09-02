package selfupdate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsUnknownVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		version string
		want    bool
	}{
		{name: "local build", version: "dev", want: true},
		{name: "released build", version: "1.4.0", want: false},
		{name: "a tag-shaped version is placeable", version: "v1.4.0", want: false},
		{name: "empty version", version: "", want: true},
		{name: "a version nothing can order", version: "nightly", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsUnknownVersion(tt.version))
		})
	}
}

func TestIsOutdatedIgnoresATagsLeadingV(t *testing.T) {
	t.Parallel()
	assert.False(t, IsOutdated("v1.4.0", "1.4.0"), "the release side is already stripped; a v on this side would reinstall the running release forever")
	assert.True(t, IsOutdated("v1.4.0", "1.5.0"))
}

func TestIsOutdated(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		current string
		latest  string
		want    bool
	}{
		{name: "same version", current: "1.4.0", latest: "1.4.0", want: false},
		{name: "older version", current: "1.3.9", latest: "1.4.0", want: true},
		{name: "a version ahead of the release is not outdated", current: "1.5.0", latest: "1.4.0", want: false},
		{name: "an rc is not outdated by the stable release it follows", current: "1.5.0-rc1", latest: "1.4.0", want: false},
		{name: "an rc is outdated by its own stable release", current: "1.5.0-rc1", latest: "1.5.0", want: true},
		{name: "an rc is outdated by a later rc", current: "1.5.0-rc1", latest: "1.5.0-rc2", want: true},
		{name: "an rc series keeps advancing past its tenth build", current: "1.5.0-rc2", latest: "1.5.0-rc10", want: true},
		{name: "a later rc is not outdated by an earlier one", current: "1.5.0-rc10", latest: "1.5.0-rc2", want: false},
		{name: "alpha is outdated by beta", current: "1.5.0-alpha.3", latest: "1.5.0-beta.1", want: true},
		{name: "a stable release is not outdated by its own rc", current: "1.4.0", latest: "1.4.0-rc1", want: false},
		{name: "fewer prerelease identifiers sort first", current: "1.5.0-rc", latest: "1.5.0-rc.1", want: true},
		{name: "build metadata does not make a version outdated", current: "1.4.0+build7", latest: "1.4.0", want: false},
		{name: "a shorter version compares as trailing zeros", current: "1.4", latest: "1.4.0", want: false},
		{name: "a fourth part still counts", current: "1.2.3.4", latest: "1.2.3.9", want: true},
		{name: "an unparsable version is outdated by any release", current: "dev", latest: "1.4.0", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsOutdated(tt.current, tt.latest))
		})
	}
}
