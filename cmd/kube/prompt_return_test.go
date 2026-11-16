package kube

import (
	"os"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKubePromptsReturnClosedInput(t *testing.T) {
	for _, name := range []string{"scopes", "duration"} {
		t.Run(name, func(t *testing.T) {
			f, err := os.Open(os.DevNull)
			require.NoError(t, err)
			saved := os.Stdin
			os.Stdin = f
			t.Cleanup(func() { os.Stdin = saved; _ = f.Close() })
			if name == "scopes" {
				_, err = promptKubeScopes()
			} else {
				_, err = promptDuration()
			}
			var exitErr *utils.ExitError
			require.ErrorAs(t, err, &exitErr)
			require.EqualError(t, err, "No input received.")
			assert.Equal(t, 1, exitErr.Code)
		})
	}
}
