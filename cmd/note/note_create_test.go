package note

import (
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoteCreateKeepsItsFlagsWhenAnotherCommandIsBuilt(t *testing.T) {
	t.Parallel()
	first := newNoteCreateCmd(cmdtest.NoClient, utils.Stdio)
	require.NoError(t, first.ParseFlags([]string{"--server", "my-server", "--content", "hello", "--private"}))

	_ = newNoteCreateCmd(cmdtest.NoClient, utils.Stdio)

	server, _ := first.Flags().GetString("server")
	content, _ := first.Flags().GetString("content")
	private, _ := first.Flags().GetBool("private")
	assert.Equal(t, "my-server", server)
	assert.Equal(t, "hello", content)
	assert.True(t, private, "--private must survive building a second command")
}
