package exec

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExceedsInlineLimit(t *testing.T) {
	assert.False(t, exceedsInlineLimit(""))
	assert.False(t, exceedsInlineLimit(strings.Repeat("a", 2048)), "2048 bytes is the inclusive inline boundary")
	assert.True(t, exceedsInlineLimit(strings.Repeat("a", 2049)))
}
