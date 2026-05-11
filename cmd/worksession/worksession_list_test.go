package worksession_test

import (
	"testing"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/stretchr/testify/assert"
)

func TestMarkActive_DecoratesMatchingRow(t *testing.T) {
	rows := []wsapi.WorkSessionAttributes{
		{ID: "ses-1", Description: "alpha", Status: "active"},
		{ID: "ses-2", Description: "beta", Status: "active"},
	}
	worksession.MarkActive(rows, "ses-2")
	assert.Equal(t, "", rows[0].Active)
	assert.Equal(t, "*", rows[1].Active)
}

func TestMarkActive_EmptyActiveUUID_NoChange(t *testing.T) {
	rows := []wsapi.WorkSessionAttributes{
		{ID: "ses-1", Description: "alpha"},
		{ID: "ses-2", Description: "beta"},
	}
	worksession.MarkActive(rows, "")
	assert.Equal(t, "", rows[0].Active)
	assert.Equal(t, "", rows[1].Active)
}

func TestMarkActive_NoMatch_NoChange(t *testing.T) {
	rows := []wsapi.WorkSessionAttributes{
		{ID: "ses-1", Description: "alpha"},
	}
	worksession.MarkActive(rows, "ses-other")
	assert.Equal(t, "", rows[0].Active)
}

func TestMarkActive_EmptySlice_NoPanic(t *testing.T) {
	var rows []wsapi.WorkSessionAttributes
	assert.NotPanics(t, func() {
		worksession.MarkActive(rows, "ses-anything")
	})
}
