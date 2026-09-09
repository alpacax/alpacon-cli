package cmd

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkSessionUpdateRejectsBlankServerBeforeLogin(t *testing.T) {
	stdout, stderr, code := runHelperProcess(t, "TestServerValidationHelperProcess", "server-validation-helper",
		[]string{"work-session", "update", "ses-test", "--server", " "}, "GO_WANT_SERVER_VALIDATION_HELPER=1", homeEnvVar()+"="+t.TempDir())
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "--server must contain at least one valid server name")
	assert.NotContains(t, stderr, "login")
}

func TestServerValidationHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_SERVER_VALIDATION_HELPER") != "1" {
		return
	}
	args, ok := helperArgsAfter(os.Args, "server-validation-helper")
	require.True(t, ok)
	RootCmd.SetArgs(args)
	Execute()
	os.Exit(0)
}
