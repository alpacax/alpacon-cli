//go:build windows

package localsignal

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWindowsToastCommand(t *testing.T) {
	t.Parallel()
	for _, link := range []string{"", "https://console.example/commands/1"} {
		name := "without link"
		if link != "" {
			name = "with link"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cmd := windowsNotificationCommand(context.Background(), "title", "body", link)
			require.Len(t, cmd.Args, 5)
			assert.Equal(t, []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand"}, cmd.Args[:4])
			data, err := base64.StdEncoding.DecodeString(cmd.Args[4])
			require.NoError(t, err)
			units := make([]uint16, len(data)/2)
			for i := range units {
				units[i] = binary.LittleEndian.Uint16(data[2*i:])
			}
			script := string(utf16.Decode(units))
			assert.Contains(t, script, `CreateToastNotifier('{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe')`)
			assert.Equal(t, link != "", strings.Contains(script, `SetAttribute('activationType', 'protocol')`))
			if link != "" {
				assert.Contains(t, script, base64.StdEncoding.EncodeToString([]byte(link)))
			}
		})
	}
}
