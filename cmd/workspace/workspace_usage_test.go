package workspace

import (
	"encoding/json"
	"testing"

	"github.com/alpacax/alpacon-cli/api/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRenderUsageEstimate_NullLimitRendersFairUseNoCap covers the D0 change: an
// Essentials fair-use axis (websh, webftp, websh-share) answers the estimate
// with a null limit, and a raw JSON `null` there reads as a bug in the
// terminal rather than as the fair-use answer it is.
func TestRenderUsageEstimate_NullLimitRendersFairUseNoCap(t *testing.T) {
	t.Parallel()

	numericLimit := 20.0
	estimate := &workspace.UsageEstimate{
		Currency: "USD",
		Services: map[string]workspace.ServiceUsage{
			"websh": {
				Name:         "Websh",
				Unit:         "hour",
				Limit:        nil,
				CurrentUsage: 42,
				CurrentCost:  "0",
			},
			"server": {
				Name:         "Server",
				Unit:         "count",
				Limit:        &numericLimit,
				CurrentUsage: 5,
				CurrentCost:  "0",
			},
		},
	}

	rendered := renderUsageEstimate(estimate)
	require.Contains(t, rendered.Services, "websh")
	require.Contains(t, rendered.Services, "server")
	assert.Equal(t, noLimitText, rendered.Services["websh"].Limit)
	assert.InDelta(t, numericLimit, rendered.Services["server"].Limit, 0)

	// The rendered value must also survive the actual JSON encoding the command
	// sends to PrintJson: a null limit becomes the string, not `null`.
	data, err := json.Marshal(rendered)
	require.NoError(t, err)

	var decoded struct {
		Services map[string]struct {
			Limit any `json:"limit"`
		} `json:"services"`
	}
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, noLimitText, decoded.Services["websh"].Limit)
	assert.InDelta(t, numericLimit, decoded.Services["server"].Limit, 0)
}
