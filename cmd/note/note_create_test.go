package note

import (
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoteCreateKeepsItsFlagsWhenAnotherCommandIsBuilt(t *testing.T) {
	t.Parallel()
	first := newNoteCreateCmd(cmdtest.NoClient)
	require.NoError(t, first.ParseFlags([]string{"--server", "my-server", "--content", "hello", "--private"}))

	_ = newNoteCreateCmd(cmdtest.NoClient)

	server, _ := first.Flags().GetString("server")
	content, _ := first.Flags().GetString("content")
	private, _ := first.Flags().GetBool("private")
	assert.Equal(t, "my-server", server)
	assert.Equal(t, "hello", content)
	assert.True(t, private, "--private must survive building a second command")
}
