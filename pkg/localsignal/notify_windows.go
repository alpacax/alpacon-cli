//go:build windows

package localsignal

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows"
)

func platformHeadless() bool {
	var session uint32
	err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &session)
	return headless("windows", os.LookupEnv, err == nil && session == 0)
}

func (platformNotifier) Notify(ctx context.Context, title, body string) error {
	// Only base64 enters script text; XML values are assigned as text nodes.
	encode := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	var script strings.Builder
	fmt.Fprint(&script, `$ErrorActionPreference='Stop'; `+
		`[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType=WindowsRuntime] > $null; `+
		`[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType=WindowsRuntime] > $null; `+
		`$xml = New-Object Windows.Data.Xml.Dom.XmlDocument; `+
		`$xml.LoadXml('<toast><visual><binding template="ToastGeneric"><text/><text/></binding></visual></toast>'); `)
	for i, value := range []string{title, body} {
		fmt.Fprintf(&script, `$xml.GetElementsByTagName('text').Item(%d).InnerText = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s')); `, i, encode(value))
	}
	fmt.Fprint(&script, `[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('Alpacon').Show([Windows.UI.Notifications.ToastNotification]::new($xml))`)
	return exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script.String()).Run()
}
