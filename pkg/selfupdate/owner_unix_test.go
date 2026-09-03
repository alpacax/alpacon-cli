//go:build !windows

package selfupdate

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecRunnerLeavesStderrOutOfTheAnswer(t *testing.T) {
	t.Parallel()
	out, err := ExecRunner("/bin/sh", "-c", `echo "dpkg-query: warning: parsing file" >&2; echo "alpacon: /usr/local/bin/alpacon"`)

	require.NoError(t, err)
	assert.Equal(t, "alpacon: /usr/local/bin/alpacon\n", string(out))
}

func TestExecRunnerSeparatesAnAnswerFromAQueryThatCouldNotAnswer(t *testing.T) {
	original := packageQueryTimeout
	t.Cleanup(func() { packageQueryTimeout = original })
	packageQueryTimeout = 100 * time.Millisecond

	_, stalled := ExecRunner("/bin/sh", "-c", "sleep 5")
	require.ErrorIs(t, stalled, ErrOwnerUnknown, "rpm is killed mid-query exactly when another transaction holds its database")

	_, notOwned := ExecRunner("/bin/sh", "-c", "exit 1")
	require.Error(t, notOwned)
	require.NotErrorIs(t, notOwned, ErrOwnerUnknown, "a non-zero exit is the answer 'this manager does not own it'")

	_, missing := ExecRunner("alpacon-no-such-package-manager", "-qf", "/usr/local/bin/alpacon")
	require.Error(t, missing)
	assert.NotErrorIs(t, missing, ErrOwnerUnknown, "a manager that is not installed here has answered too")
}
