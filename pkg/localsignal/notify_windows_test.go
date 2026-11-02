//go:build windows

package localsignal

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWindowsToastCommand(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, link string
		clickable  bool
	}{
		{"without link", "", false},
		{"https", "https://console.example/commands/1", true},
		{"file", "file:///tmp/approval", false},
		{"search", "search-ms:query=approval", false},
		{"settings", "ms-settings:privacy", false},
		{"http", "http://console.example/commands/1", false},
		{"relative", "/commands/1", false},
		{"javascript", "javascript:alert(1)", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			link := consoleLink(tc.link, "https://console.example", "")
			cmd, err := windowsNotificationCommand(context.Background(), "title", "body", link)
			require.NoError(t, err)
			require.Len(t, cmd.Args, 5)
			assert.Equal(t, []string{cmd.Path, "-NoProfile", "-NonInteractive", "-EncodedCommand"}, cmd.Args[:4])
			data, err := base64.StdEncoding.DecodeString(cmd.Args[4])
			require.NoError(t, err)
			units := make([]uint16, len(data)/2)
			for i := range units {
				units[i] = binary.LittleEndian.Uint16(data[2*i:])
			}
			script := string(utf16.Decode(units))
			assert.Contains(t, script, `CreateToastNotifier('{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe')`)
			assert.Equal(t, tc.clickable, strings.Contains(script, `SetAttribute('activationType', 'protocol')`))
			if tc.clickable {
				assert.Contains(t, script, base64.StdEncoding.EncodeToString([]byte(tc.link)))
			} else {
				assert.NotContains(t, script, `SetAttribute('launch'`)
			}
		})
	}
}

func platformNotificationArguments(ctx context.Context, title, body, link string) []string {
	cmd, err := windowsNotificationCommand(ctx, title, body, link)
	if err != nil {
		panic(err)
	}
	data, err := base64.StdEncoding.DecodeString(cmd.Args[4])
	if err != nil {
		panic(err)
	}
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(data[2*i:])
	}
	return []string{string(utf16.Decode(units))}
}

func TestWindowsNotifierPath(t *testing.T) {
	old := getSystemDirectory
	t.Cleanup(func() { getSystemDirectory = old })
	getSystemDirectory = func() (string, error) { return `D:\Windows\System32`, nil }
	t.Setenv("SystemRoot", `C:\other`)
	cmd, err := windowsNotificationCommand(context.Background(), "title", "body", "")
	require.NoError(t, err)
	assert.Equal(t, `D:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, cmd.Path)
	getSystemDirectory = func() (string, error) { return "", errors.New("unavailable") }
	cmd, err = windowsNotificationCommand(context.Background(), "title", "body", "")
	require.Error(t, err)
	assert.Nil(t, cmd)
}
