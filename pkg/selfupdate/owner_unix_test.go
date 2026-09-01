//go:build !windows

package selfupdate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecRunnerLeavesStderrOutOfTheAnswer(t *testing.T) {
	out, err := ExecRunner("/bin/sh", "-c", `echo "dpkg-query: warning: parsing file" >&2; echo "alpacon: /usr/local/bin/alpacon"`)

	require.NoError(t, err)
	assert.Equal(t, "alpacon: /usr/local/bin/alpacon\n", string(out))
}
