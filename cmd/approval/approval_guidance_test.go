package approval

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApprovalGuidanceKeepsConsolePointer(t *testing.T) {
	var help bytes.Buffer
	ApprovalCmd.SetOut(&help)
	ApprovalCmd.SetErr(&help)
	t.Cleanup(func() {
		ApprovalCmd.SetOut(nil)
		ApprovalCmd.SetErr(nil)
	})

	err := ApprovalCmd.RunE(ApprovalCmd, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Alpacon console")
}
