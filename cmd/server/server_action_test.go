package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBusyGuardMessage(t *testing.T) {
	tests := []struct {
		name        string
		force       bool
		wantContain string
		wantAbsent  string
	}{
		{
			name:        "without force suggests --force",
			force:       false,
			wantContain: "pass --force to override",
			wantAbsent:  "despite --force",
		},
		{
			name:        "with force does not re-suggest --force",
			force:       true,
			wantContain: "despite --force",
			wantAbsent:  "pass --force to override",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := busyGuardMessage("my-server", tt.force)
			assert.Contains(t, msg, "my-server")
			assert.Contains(t, msg, tt.wantContain)
			assert.NotContains(t, msg, tt.wantAbsent)
		})
	}
}
