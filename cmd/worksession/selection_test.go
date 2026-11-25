package worksession

import (
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

func TestOperationScopes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		operation Operation
		want      []string
	}{
		{name: "command", operation: Command, want: []string{"command"}},
		{name: "file command", operation: FileCommand, want: []string{"command", "webftp"}},
		{name: "websh", operation: Websh, want: []string{"websh"}},
		{name: "transfer", operation: Transfer, want: []string{"webftp"}},
		{name: "tunnel", operation: Tunnel, want: []string{"tunnel"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, (Need{Operation: tt.operation}).scopes())
		})
	}
}

func TestFixedNeverAddsAnOffer(t *testing.T) {
	t.Parallel()
	callbacks := Fixed("session-1").Offer(utils.ErrorHandlerCallbacks{})
	assert.Nil(t, callbacks.OnWorkSessionRefused)
	assert.Equal(t, "session-1", Fixed("session-1").ID())
	assert.Empty(t, (*Selection)(nil).ID())
}
