package cmdtest

import (
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

// Serial: it replaces os.Args, which Cobra reads in place of a nil argument list.
func TestRunWithoutArgumentsIgnoresTheTestBinarysArguments(t *testing.T) {
	saved := os.Args
	os.Args = []string{"cmdtest.test", "-test.run", "TestX"} // a space-separated flag leaves TestX positional
	t.Cleanup(func() { os.Args = saved })
	ran := false
	cmd := &cobra.Command{Use: "probe", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		ran = true
		return nil
	}}

	failure, code := Run(cmd)

	assert.Equal(t, 0, code, failure)
	assert.True(t, ran, "the command must run")
}
