package cmd

import (
	"encoding/json"
	"testing"

	"github.com/alpacax/alpacon-cli/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWhoamiSessionGuidanceForCommandAndTransferAccess(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		cfg      config.Config
		required bool
	}{
		{name: "personal token", cfg: config.Config{Token: "personal-token"}, required: true},
		{name: "browser login", cfg: config.Config{AccessToken: "browser-token"}, required: true},
		{name: "service token", cfg: config.Config{Token: config.ServiceTokenPrefix + "example"}},
		{name: "unknown authentication", cfg: config.Config{}},
		{name: "browser login takes precedence", cfg: config.Config{AccessToken: "browser-token", Token: config.ServiceTokenPrefix + "example"}, required: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body, err := json.Marshal(whoamiOutput{WorksessionRequired: isWorksessionRequired(tt.cfg)})
			require.NoError(t, err)
			var guidance struct {
				Canonical bool `json:"worksession_required_for_access"`
				Legacy    bool `json:"work_session_required"`
			}
			require.NoError(t, json.Unmarshal(body, &guidance))
			assert.Equal(t, tt.required, guidance.Canonical)
			assert.Equal(t, tt.required, guidance.Legacy)
		})
	}
}
