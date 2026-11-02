//go:build windows

package localsignal

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

func platformHeadless() bool {
	var session uint32
	err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &session)
	return headless("windows", os.LookupEnv, err == nil && session == 0)
}

func (platformNotifier) Notify(ctx context.Context, title, body, consoleURL string) error {
	return windowsNotificationCommand(ctx, title, body, consoleURL).Run()
}

func windowsNotificationCommand(ctx context.Context, title, body, consoleURL string) *exec.Cmd {
	// Only base64 enters script text; XML values are assigned as text nodes.
	encode := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	var script strings.Builder
	fmt.Fprint(&script, `$ErrorActionPreference='Stop'; `+
		`[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType=WindowsRuntime] > $null; `+
		`[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType=WindowsRuntime] > $null; `+
		`$xml = New-Object Windows.Data.Xml.Dom.XmlDocument; `+
		`$xml.LoadXml('<toast><visual><binding template="ToastGeneric"><text/><text/></binding></visual></toast>'); `)
	for i, value := range []string{clean(title), clean(body)} {
		fmt.Fprintf(&script, `$xml.GetElementsByTagName('text').Item(%d).InnerText = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s')); `, i, encode(value))
	}
	if consoleURL != "" {
		fmt.Fprintf(&script, `$xml.DocumentElement.SetAttribute('launch', [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s'))); $xml.DocumentElement.SetAttribute('activationType', 'protocol'); `, encode(clean(consoleURL)))
	}
	fmt.Fprint(&script, `[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe').Show([Windows.UI.Notifications.ToastNotification]::new($xml))`)
	root := os.Getenv("SystemRoot")
	if !filepath.IsAbs(root) {
		root = `C:\Windows`
	}
	return exec.CommandContext(ctx, filepath.Join(root, `System32\WindowsPowerShell\v1.0\powershell.exe`), "-NoProfile", "-NonInteractive", "-EncodedCommand", encodePowerShell(script.String()))
}

func encodePowerShell(script string) string {
	units := utf16.Encode([]rune(script))
	data := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[2*i:], unit)
	}
	return base64.StdEncoding.EncodeToString(data)
}
