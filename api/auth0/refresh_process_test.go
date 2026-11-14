package auth0

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/httpclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Serial: the child process uses the parent's temporary HOME and an isolated login.
func TestRefreshAccessTokenAcrossProcesses(t *testing.T) {
	if os.Getenv("ALPACON_REFRESH_CHILD") == "1" {
		started := loadedRefreshConfig(t)
		_, err := fmt.Fprintln(os.Stdout, "ready")
		require.NoError(t, err)
		var gate [1]byte
		_, err = io.ReadFull(os.Stdin, gate[:])
		require.NoError(t, err)
		result, err := RefreshAccessToken(started.WorkspaceURL, httpclient.New(true), started)
		require.NoError(t, err)
		assert.Equal(t, "rotated-access", result.AccessToken)
		assert.Equal(t, "rotated-refresh", result.RefreshToken)
		return
	}
	server := newRefreshServer(t, func(tokenRequest) (int, string) {
		return http.StatusOK, `{"access_token":"rotated-access","refresh_token":"rotated-refresh","expires_in":3600}`
	})
	setupRefreshConfig(t, server)
	type child struct {
		cmd    *exec.Cmd
		gate   io.WriteCloser
		output *bufio.Reader
		stderr strings.Builder
	}
	children := make([]*child, 2)
	for i := range children {
		c := &child{cmd: exec.Command(os.Args[0], "-test.run=^TestRefreshAccessTokenAcrossProcesses$", "-test.timeout=30s")}
		c.cmd.Env = append(os.Environ(), "ALPACON_REFRESH_CHILD=1")
		c.cmd.Stderr = &c.stderr
		gate, err := c.cmd.StdinPipe()
		require.NoError(t, err)
		c.gate = gate
		stdout, err := c.cmd.StdoutPipe()
		require.NoError(t, err)
		c.output = bufio.NewReader(stdout)
		require.NoError(t, c.cmd.Start())
		t.Cleanup(func() { _ = c.cmd.Process.Kill() })
		children[i] = c
	}
	for _, c := range children {
		line, err := c.output.ReadString('\n')
		require.NoError(t, err)
		require.Equal(t, "ready\n", line)
	}
	for _, c := range children {
		_, err := c.gate.Write([]byte{1})
		require.NoError(t, err)
		require.NoError(t, c.gate.Close())
	}
	for _, c := range children {
		output, err := io.ReadAll(c.output)
		require.NoError(t, err)
		require.NoError(t, c.cmd.Wait(), "%s%s", output, c.stderr.String())
	}
	assert.Len(t, server.exchanges(), 1)
	stored := loadedRefreshConfig(t)
	assert.Equal(t, "rotated-access", stored.AccessToken)
	assert.Equal(t, "rotated-refresh", stored.RefreshToken)
}
