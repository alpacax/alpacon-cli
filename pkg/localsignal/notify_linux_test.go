//go:build linux

package localsignal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLinuxMarkupEscaping(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "a &amp; b &lt;tag&gt;", escapeMarkup("a & b <tag>"))
}

func platformNotificationArguments(ctx context.Context, title, body, _ string) []string {
	return linuxNotificationCommand(ctx, "/usr/bin/notify-send", title, body).Args
}
