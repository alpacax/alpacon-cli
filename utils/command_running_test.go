package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPrintCommandRunningWritesStatusObjectUnderJSON(t *testing.T) {
	t.Parallel()
	// Given
	ios, out, errOut := TestIOStreams()
	_ = ios.SetOutputFormat(OutputFormatJSON)

	// When
	ios.PrintCommandRunning("job-1", "queued")

	// Then
	assert.JSONEq(t, `{
		"ok": true,
		"status": "running",
		"exit_code": 0,
		"message": "command is still running (status: queued).",
		"command_id": "job-1",
		"context": {"command_id": "job-1", "command_status": "queued"},
		"next_actions": [{"command": "alpacon exec logs job-1", "description": "Read the result once the command finishes; do not re-submit it"}]
	}`, out.String())
	assert.Empty(t, errOut.String())
}

func TestPrintCommandRunningStripsServerControlTextInBothFormats(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, format, wantOut, wantErr string
	}{
		{
			name:    "table",
			format:  OutputFormatTable,
			wantErr: "command is still running (status: running).\nRun `alpacon exec logs job-1` again to check later.\n",
		},
		{
			name:    "json",
			format:  OutputFormatJSON,
			wantOut: `{"ok":true,"status":"running","exit_code":0,"message":"command is still running (status: running).","command_id":"job-1","context":{"command_id":"job-1","command_status":"running"},"next_actions":[{"command":"alpacon exec logs job-1","description":"Read the result once the command finishes; do not re-submit it"}]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Given
			ios, out, errOut := TestIOStreams()
			_ = ios.SetOutputFormat(tt.format)

			// When
			ios.PrintCommandRunning("job-1\x1b[2K\u202e", "run\x1b[31mning")

			// Then
			if tt.wantOut == "" {
				assert.Empty(t, out.String())
			} else {
				assert.JSONEq(t, tt.wantOut, out.String())
			}
			assert.Equal(t, tt.wantErr, errOut.String())
		})
	}
}
