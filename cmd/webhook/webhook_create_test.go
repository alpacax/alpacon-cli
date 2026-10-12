package webhook

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOwnerFlagIsDeprecated(t *testing.T) {
	flag := webhookCreateCmd.Flags().Lookup("owner")
	require.NotNil(t, flag)
	assert.NotEmpty(t, flag.Deprecated)
}

func TestDetectProviderFromURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{"slack", "https://hooks.slack.com/services/T00/B00/xxx", "slack"},
		{"discord", "https://discord.com/api/webhooks/123/abc", "discord"},
		{"teams", "https://myorg.webhook.office.com/webhookb2/xxx", "teams"},
		{"teams workflows", "https://prod-00.westus.logic.azure.com:443/workflows/xxx", "teams"},
		{"telegram", "https://api.telegram.org/bot123/sendMessage", "telegram"},
		{"custom url", "https://example.com/webhook", "custom"},
		{"empty url", "", "custom"},
		{"discord wrong path", "https://discord.com/other/path", "discord"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectProviderFromURL(tt.url)
			if got != tt.expected {
				t.Errorf("detectProviderFromURL(%q) = %q, want %q", tt.url, got, tt.expected)
			}
		})
	}
}
