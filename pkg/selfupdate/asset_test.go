package selfupdate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArchiveName(t *testing.T) {
	tests := []struct {
		name   string
		goos   string
		goarch string
		want   string
	}{
		{name: "linux amd64", goos: "linux", goarch: "amd64", want: "alpacon-1.4.0-linux-amd64.tar.gz"},
		{name: "darwin arm64", goos: "darwin", goarch: "arm64", want: "alpacon-1.4.0-darwin-arm64.tar.gz"},
		{name: "windows amd64 is a zip", goos: "windows", goarch: "amd64", want: "alpacon-1.4.0-windows-amd64.zip"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ArchiveName("1.4.0", tt.goos, tt.goarch))
		})
	}
}

func TestChecksumsName(t *testing.T) {
	assert.Equal(t, "alpacon-1.4.0-checksums.sha256", ChecksumsName("1.4.0"))
}

func TestSelectAsset(t *testing.T) {
	release := &Release{
		Version: "1.4.0",
		Assets: []Asset{
			{Name: "alpacon-1.4.0-linux-amd64.tar.gz", DownloadURL: "https://example.test/a.tar.gz"},
		},
	}

	found, err := SelectAsset(release, "alpacon-1.4.0-linux-amd64.tar.gz")
	require.NoError(t, err)
	assert.Equal(t, "https://example.test/a.tar.gz", found.DownloadURL)

	_, err = SelectAsset(release, "alpacon-1.4.0-windows-arm64.zip")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "alpacon-1.4.0-windows-arm64.zip")
}

func TestBinaryNameIgnoresWhatTheUserRenamedTheBinaryTo(t *testing.T) {
	assert.Equal(t, "alpacon", BinaryName("linux"))
	assert.Equal(t, "alpacon", BinaryName("darwin"))
	assert.Equal(t, "alpacon.exe", BinaryName("windows"))
}

func TestAssetNamesStillMatchTheGoreleaserTemplates(t *testing.T) {
	config, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yaml"))
	require.NoError(t, err)

	archives := goreleaserSection(string(config), "archives:")
	checksum := goreleaserSection(string(config), "checksum:")

	assert.Contains(t, archives, `- name_template: '{{ .ProjectName }}-{{ trimprefix .Version "v" }}-{{ .Os }}-{{ .Arch }}'`,
		"ArchiveName in asset.go builds this name; change both together")
	assert.Contains(t, checksum, `name_template: '{{ .ProjectName }}-{{ trimprefix .Version "v" }}-checksums.sha256'`,
		"ChecksumsName in asset.go builds this name; change both together")
	assert.Contains(t, archives, "- goos: windows",
		"ArchiveName asks for a .zip on windows, and only this override builds one")

	var formats []string
	for _, line := range archives {
		if strings.HasPrefix(line, "format:") {
			formats = append(formats, line)
		}
	}
	assert.Equal(t, []string{"format: zip"}, formats,
		"ArchiveName builds .tar.gz everywhere but windows; an archives-level format would change that too")
}

func goreleaserSection(config, key string) []string {
	var lines []string
	inside := false
	for raw := range strings.SplitSeq(config, "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		switch {
		case !strings.HasPrefix(line, " ") && trimmed == key:
			inside = true
		case inside && trimmed != "" && !strings.HasPrefix(line, " "):
			return lines
		case inside && trimmed != "":
			lines = append(lines, trimmed)
		}
	}
	return lines
}
