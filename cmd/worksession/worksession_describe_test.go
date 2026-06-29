package worksession

import (
	"testing"

	"github.com/alpacax/alpacon-cli/api/types"
	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/stretchr/testify/assert"
)

func rowValue(rows []describeRow, field string) (string, bool) {
	for _, r := range rows {
		if r.Field == field {
			return r.Value, true
		}
	}
	return "", false
}

func TestDescribeRowsIncludesAdjustmentsAndRecommendations(t *testing.T) {
	session := &wsapi.WorkSession{
		ID:      "ses-1",
		Status:  "active",
		Scopes:  []string{"command"},
		Servers: []types.ServerSummary{{Name: "web-01"}},
		Adjustments: &wsapi.WorkSessionAdjustments{
			Scopes:  &wsapi.ScopeDiff{Old: []string{"command", "logs"}, New: []string{"command"}},
			Servers: &wsapi.ServerDiff{Old: []types.ServerSummary{{Name: "web-01"}, {Name: "db-01"}}, New: []types.ServerSummary{{Name: "web-01"}}},
		},
		Recommendations: []wsapi.WorkSessionRecommendation{
			{Severity: "high", Source: "admin_added", Text: "a"},
			{Severity: "low", Source: "ai_suggested", Text: "b"},
		},
	}
	rows := describeRows(session)

	v, ok := rowValue(rows, "Scopes adjusted")
	assert.True(t, ok)
	assert.Equal(t, "command, logs → command", v)
	v, ok = rowValue(rows, "Servers adjusted")
	assert.True(t, ok)
	assert.Equal(t, "web-01, db-01 → web-01", v)
	v, ok = rowValue(rows, "Recommendations")
	assert.True(t, ok)
	assert.Equal(t, "[high] (admin_added) a; [low] (ai_suggested) b", v)
}

func TestDescribeRowsOmitsAbsentAdjustments(t *testing.T) {
	rows := describeRows(&wsapi.WorkSession{ID: "ses-1", Status: "active"})
	_, ok := rowValue(rows, "Scopes adjusted")
	assert.False(t, ok)
	_, ok = rowValue(rows, "Recommendations")
	assert.False(t, ok)
}
