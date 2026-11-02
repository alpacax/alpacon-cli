//go:build linux

package localsignal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLinuxMarkupEscaping(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "a &amp; b &lt;tag&gt;", escapeMarkup("a & b <tag>"))
}
