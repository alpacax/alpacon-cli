package worksession

import (
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/aiagent"
	"github.com/stretchr/testify/assert"
)

func TestBuildSudoPolicies(t *testing.T) {
	for _, name := range aiagent.Variables() {
		t.Setenv(name, "")
	}
	t.Setenv("AI_AGENT", "codex")
	t.Run("one policy per --sudo value, commands split on comma", func(t *testing.T) {
		policies := buildSudoPolicies(
			[]string{"systemctl restart nginx,systemctl reload nginx", "tail -f /var/log/nginx/*.log"},
			"hotfix",
		)
		assert.Len(t, policies, 2)
		assert.Equal(t, []string{"systemctl restart nginx", "systemctl reload nginx"}, policies[0].Commands)
		assert.True(t, policies[0].AllowBypassMFA)
		assert.Equal(t, "hotfix", policies[0].Reason)
		assert.Equal(t, []string{"tail -f /var/log/nginx/*.log"}, policies[1].Commands)
	})

	t.Run("empty and whitespace-only values are skipped", func(t *testing.T) {
		assert.Empty(t, buildSudoPolicies(nil, ""))
		assert.Empty(t, buildSudoPolicies([]string{"", " , "}, ""))
	})
}
