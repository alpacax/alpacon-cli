package worksession_test

import (
	"testing"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/stretchr/testify/assert"
)

func TestMarkActive_DecoratesMatchingRow(t *testing.T) {
	t.Parallel()
	rows := []wsapi.WorkSessionAttributes{
		{ID: "ses-1", Description: "alpha", Status: "active"},
		{ID: "ses-2", Description: "beta", Status: "active"},
	}
	worksession.MarkActive(rows, "ses-2")
	assert.Empty(t, rows[0].Active)
	assert.Equal(t, "*", rows[1].Active)
}

func TestMarkActive_MatchesIDThatDiffersOnlyInCase(t *testing.T) {
	t.Parallel()
	rows := []wsapi.WorkSessionAttributes{
		{ID: "4f9c2a1e-0000-4000-8000-00000000abcd", Status: "active"},
	}

	worksession.MarkActive(rows, "4F9C2A1E-0000-4000-8000-00000000ABCD")

	assert.Equal(t, "*", rows[0].Active)
}

func TestMarkActive_EmptyActiveUUID_NoChange(t *testing.T) {
	t.Parallel()
	rows := []wsapi.WorkSessionAttributes{
		{ID: "ses-1", Description: "alpha"},
		{ID: "ses-2", Description: "beta"},
	}
	worksession.MarkActive(rows, "")
	assert.Empty(t, rows[0].Active)
	assert.Empty(t, rows[1].Active)
}

func TestMarkActive_NoMatch_NoChange(t *testing.T) {
	t.Parallel()
	rows := []wsapi.WorkSessionAttributes{
		{ID: "ses-1", Description: "alpha"},
	}
	worksession.MarkActive(rows, "ses-other")
	assert.Empty(t, rows[0].Active)
}

func TestMarkActive_EmptySlice_NoPanic(t *testing.T) {
	t.Parallel()
	var rows []wsapi.WorkSessionAttributes
	assert.NotPanics(t, func() {
		worksession.MarkActive(rows, "ses-anything")
	})
}

func TestMarkActive_SkipsSessionThatHasEnded(t *testing.T) {
	t.Parallel()
	for _, status := range []string{"completed", "expired", "revoked", "rejected", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()
			rows := []wsapi.WorkSessionAttributes{{ID: "ses-1", Status: status}}

			worksession.MarkActive(rows, "ses-1")

			assert.Empty(t, rows[0].Active)
		})
	}
}
