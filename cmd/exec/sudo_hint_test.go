package exec

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSudoDenialHint(t *testing.T) {
	t.Run("returns guidance when denial code present", func(t *testing.T) {
		out := "sudo: a password is required\nsudo_no_worksession_policy\n"
		hint := sudoDenialHint(out)
		assert.NotEmpty(t, hint)
		assert.True(t, strings.Contains(hint, "work-session update --sudo"),
			"hint should point to the work-session update command")
	})

	t.Run("empty when no denial code", func(t *testing.T) {
		assert.Empty(t, sudoDenialHint("ok\n"))
		assert.Empty(t, sudoDenialHint(""))
	})
}
