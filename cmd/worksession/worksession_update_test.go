package worksession

import (
	"strings"
	"testing"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/stretchr/testify/assert"
)

func TestValidateSessionForSudoUpdate(t *testing.T) {
	t.Run("pending session is rejected with actionable message", func(t *testing.T) {
		err := validateSessionForSudoUpdate(&wsapi.WorkSession{
			ID:     "ses-pending",
			Status: pendingWorkSessionStatus,
			Scopes: []string{"command", "sudo"},
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "ses-pending")
		assert.Contains(t, err.Error(), "pending")
		assert.Contains(t, err.Error(), "--sudo")
	})

	t.Run("missing sudo scope is rejected with guidance", func(t *testing.T) {
		err := validateSessionForSudoUpdate(&wsapi.WorkSession{
			ID:     "ses-no-sudo",
			Status: "active",
			Scopes: []string{"command"},
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "ses-no-sudo")
		assert.Contains(t, err.Error(), "'sudo' scope")
		// Guidance must point at the create flag, not at a separate scope flag.
		assert.True(t, strings.Contains(err.Error(), "--sudo"),
			"guidance should reference --sudo so the user creates the right session next time")
	})

	t.Run("active session with sudo scope passes", func(t *testing.T) {
		assert.NoError(t, validateSessionForSudoUpdate(&wsapi.WorkSession{
			ID:     "ses-ok",
			Status: "active",
			Scopes: []string{"command", "sudo"},
		}))
	})

	t.Run("approved session with sudo scope passes (pre-active is allowed)", func(t *testing.T) {
		assert.NoError(t, validateSessionForSudoUpdate(&wsapi.WorkSession{
			ID:     "ses-approved",
			Status: "approved",
			Scopes: []string{"sudo"},
		}))
	})
}
