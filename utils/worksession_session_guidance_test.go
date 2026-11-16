package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWorkSessionTokenNoteForCommandAndTransfer(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		operation string
	}{
		{name: "command", operation: OperationCommand},
		{name: "file transfer", operation: OperationWebFTP},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := workSessionTokenNote(tt.operation)
			assert.Contains(t, got, "personal API tokens require an active, approved WorkSession")
			assert.Contains(t, got, "Service tokens use endpoint-specific authorization")
			assert.NotContains(t, got, "bypass this check")
		})
	}
}
