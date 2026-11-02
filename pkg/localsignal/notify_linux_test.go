//go:build linux

package localsignal

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLinuxMarkupEscaping(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "a &amp; b &lt;tag&gt;", escapeMarkup("a & b <tag>"))
}

func platformNotificationArguments(ctx context.Context, title, body, _ string) []string {
	return linuxNotificationCommand(ctx, "/usr/bin/notify-send", title, body).Args
}

func TestLinuxNotifierIgnoresPATH(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "notify-send")
	require.NoError(t, os.WriteFile(fake, []byte("#!/bin/sh\nexit 99\n"), 0700))
	t.Setenv("PATH", dir)
	tool := linuxNotifierPath()
	assert.NotEqual(t, fake, tool)
	if tool != "" {
		assert.Contains(t, []string{"/usr/bin/notify-send", "/bin/notify-send"}, tool)
	}
}
