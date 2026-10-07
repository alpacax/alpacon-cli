package worksession

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkSessionGuidanceKeepsConsolePointer(t *testing.T) {
	var help bytes.Buffer
	WorkSessionCmd.SetOut(&help)
	WorkSessionCmd.SetErr(&help)
	t.Cleanup(func() {
		WorkSessionCmd.SetOut(nil)
		WorkSessionCmd.SetErr(nil)
	})

	err := WorkSessionCmd.RunE(WorkSessionCmd, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Alpacon console")
}
