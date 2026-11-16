package worksession

import (
	"net/http"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

func TestWorkSessionRevokeCmd_ClearsActiveSessionWhenItIsTheRevokedOne(t *testing.T) {
	for _, format := range completeFormats {
		t.Run(format, func(t *testing.T) {
			ts := endSessionServer(t, "ses-1", "revoke", http.StatusOK, `{}`)
			home := completeHomeWithActiveSessions(t, ts.URL, map[string]string{"test": "ses-1"})

			stdout, stderr, exitCode := runWorkSessionHelperWithEnv(t, []string{"HOME=" + home},
				format, ts.URL, "revoke", "ses-1")

			assert.Equal(t, 0, exitCode)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, "Work session ses-1 revoked.")
			assert.Contains(t, stderr, "Active work-session cleared.")
			assert.Empty(t, completeActiveSessions(t, home))
		})
	}
}

func TestWorkSessionRevokeCmd_ServerRefusalFailsAndKeepsActiveSession(t *testing.T) {
	for _, format := range completeFormats {
		t.Run(format, func(t *testing.T) {
			ts := endSessionServer(t, "ses-1", "revoke", http.StatusForbidden,
				`{"code":"work_session_not_usable","source":"work_session","detail":"not usable"}`)
			home := completeHomeWithActiveSessions(t, ts.URL, map[string]string{"test": "ses-1"})

			stdout, stderr, exitCode := runWorkSessionHelperWithEnv(t, []string{"HOME=" + home},
				format, ts.URL, "revoke", "ses-1")

			assert.Equal(t, utils.ExitCodeGeneralError, exitCode)
			assertCommandFailure(t, format, opRevoke, stdout, stderr, utils.ExitCodeGeneralError,
				"work_session_not_usable", "Failed to revoke work session")
			assert.Equal(t, map[string]string{"test": "ses-1"}, completeActiveSessions(t, home))
		})
	}
}

func TestWorkSessionRevokeCmd_InvalidSessionIDIsRefusedBeforeAnyRequest(t *testing.T) {
	for _, format := range completeFormats {
		t.Run(format, func(t *testing.T) {
			ts := testutil.NewNoRequestServer(t)

			stdout, stderr, exitCode := runWorkSessionHelper(t, format, ts.URL, "revoke", "../x")

			assert.Equal(t, utils.ExitCodeUsageError, exitCode)
			assertCommandFailure(t, format, opRevoke, stdout, stderr, utils.ExitCodeUsageError,
				utils.ErrorCodeUsage, "must be a single path segment")
		})
	}
}
