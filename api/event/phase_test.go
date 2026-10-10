package event

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

var filePhases = []string{
	"file_hash_mismatch",
	"file_payload_invalid",
	"file_open_failed",
	"file_exec_unsupported",
	"file_too_large",
}

func TestFilePhasesHaveDescriptionAndHint(t *testing.T) {
	t.Parallel()
	for _, phase := range filePhases {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			assert.Contains(t, phaseDescriptions, phase)
			assert.NotEqual(t, phase, DescribePhase(phase))
			assert.Contains(t, phaseHints, phase)
			assert.NotEmpty(t, PhaseHint(phase))
			assert.NotEqual(t, genericFilePhaseDescription, DescribePhase(phase))
			assert.NotEqual(t, genericFilePhaseHint, PhaseHint(phase))
		})
	}
}

func TestUnknownFilePhaseFallsBackToGeneric(t *testing.T) {
	t.Parallel()
	assert.Equal(t, genericFilePhaseDescription, DescribePhase("file_something_new"))
	assert.Equal(t, genericFilePhaseHint, PhaseHint("file_something_new"))
}

func TestNonFilePhasesUnchanged(t *testing.T) {
	t.Parallel()
	assert.Equal(t, phaseDescriptions["agent_timeout"], DescribePhase("agent_timeout"))
	assert.Equal(t, "something_else", DescribePhase("something_else"))
	assert.Empty(t, PhaseHint("agent_timeout"))
	assert.Empty(t, PhaseHint("something_else"))
	assert.Empty(t, PhaseHint("profile_missing"))
}

func TestStatusOnlyFailureCarriesFilePhaseHint(t *testing.T) {
	t.Parallel()
	phase := "file_too_large"
	err := errorFromDetails(EventDetails{Status: "error", ErrorPhase: &phase})
	assert.ErrorContains(t, err, "Hint: "+PhaseHint(phase))

	other := "agent_timeout"
	err = errorFromDetails(EventDetails{Status: "stuck", ErrorPhase: &other})
	assert.NotContains(t, err.Error(), "Hint:")
}
