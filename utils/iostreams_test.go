package utils

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type streamRow struct {
	Name string `json:"name"`
}

func TestIOStreamsPrintTableWritesToItsOwnOut(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		format string
		want   string
	}{
		{name: "table", format: OutputFormatTable, want: "NAME\nmy-server\n"},
		{name: "json", format: OutputFormatJSON, want: "[\n  {\n    \"name\": \"my-server\"\n  }\n]\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ios, out, errOut := TestIOStreams()
			require.NoError(t, ios.SetOutputFormat(tt.format))

			err := ios.PrintTable([]streamRow{{Name: "my-server"}})

			require.NoError(t, err)
			assert.Equal(t, tt.want, out.String())
			assert.Empty(t, errOut.String())
		})
	}
}

func TestIOStreamsMessagesGoToErrOutInItsColor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		color bool
		want  string
	}{
		{name: "plain", want: "Warning: disk is full\n"},
		{name: "colored", color: true, want: ansiBold + ansiYellow + "Warning" + ansiReset + ": disk is full\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ios, out, errOut := TestIOStreams()
			ios.ColorEnabled = tt.color

			ios.Warning("disk is %s", "full")

			assert.Equal(t, tt.want, errOut.String())
			assert.Empty(t, out.String())
		})
	}
}

func TestIOStreamsPrintsAFailureInItsOwnFormat(t *testing.T) {
	t.Parallel()
	ios, _, errOut := TestIOStreams()
	require.NoError(t, ios.SetOutputFormat(OutputFormatJSON))
	var exitErr *ExitError
	require.ErrorAs(t, NewExitError("Failed to retrieve the notes: %s.", "unavailable"), &exitErr)

	ios.PrintExitError(exitErr)

	var envelope JSONErrorEnvelope[map[string]any]
	require.NoError(t, json.Unmarshal(errOut.Bytes(), &envelope), errOut.String())
	assert.Equal(t, ExitCodeGeneralError, envelope.ExitCode)
	assert.Equal(t, "Failed to retrieve the notes: unavailable.", envelope.Message)
	assert.NotEqual(t, OutputFormatJSON, OutputFormat, "the streams' format must not leak into the flag's")
}

func TestIOStreamsRefusesAnUnknownFormat(t *testing.T) {
	t.Parallel()
	ios, _, _ := TestIOStreams()

	err := ios.SetOutputFormat("yaml")

	require.Error(t, err)
	assert.True(t, IsUsageError(err), "an --output value is the user's to fix: %v", err)
	assert.Equal(t, OutputFormatTable, ios.OutputFormat())
}

func TestColorFollowsTheTerminalUnlessNoColorIsSet(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		noColor  string
		terminal bool
		want     bool
	}{
		{name: "terminal", terminal: true, want: true},
		{name: "terminal with NO_COLOR", noColor: "1", terminal: true},
		{name: "pipe", noColor: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, colorAllowed(tt.noColor, tt.terminal))
		})
	}
}
