package worksession

import (
	"net/http"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

func TestWorkSessionActivateCmd_ActivatesTheSession(t *testing.T) {
	for _, format := range completeFormats {
		t.Run(format, func(t *testing.T) {
			ts := endSessionServer(t, "ses-1", "activate", http.StatusOK, `{}`)

			stdout, stderr, exitCode := runWorkSessionHelper(t, format, ts.URL, "activate", "ses-1")

			assert.Equal(t, 0, exitCode)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, "Work session ses-1 activated.")
		})
	}
}

func TestWorkSessionActivateCmd_ServerRefusalFails(t *testing.T) {
	for _, format := range completeFormats {
		t.Run(format, func(t *testing.T) {
			ts := endSessionServer(t, "ses-1", "activate", http.StatusForbidden,
				`{"code":"work_session_not_usable","source":"work_session","detail":"not usable"}`)

			stdout, stderr, exitCode := runWorkSessionHelper(t, format, ts.URL, "activate", "ses-1")

			assert.Equal(t, utils.ExitCodeGeneralError, exitCode)
			assertCommandFailure(t, format, opActivate, stdout, stderr, utils.ExitCodeGeneralError,
				"work_session_not_usable", "Failed to activate work session")
		})
	}
}

func TestWorkSessionActivateCmd_InvalidSessionIDIsRefusedBeforeAnyRequest(t *testing.T) {
	for _, format := range completeFormats {
		t.Run(format, func(t *testing.T) {
			ts := testutil.NewNoRequestServer(t)

			stdout, stderr, exitCode := runWorkSessionHelper(t, format, ts.URL, "activate", "../x")

			assert.Equal(t, utils.ExitCodeUsageError, exitCode)
			assertCommandFailure(t, format, opActivate, stdout, stderr, utils.ExitCodeUsageError,
				utils.ErrorCodeUsage, "must be a single path segment")
		})
	}
}

func TestWorkSessionActivateCmd_MissingSessionIDIsAUsageError(t *testing.T) {
	ts := testutil.NewNoRequestServer(t)

	_, stderr, exitCode := runWorkSessionHelper(t, utils.OutputFormatTable, ts.URL, "activate")

	assert.Equal(t, utils.ExitCodeUsageError, exitCode)
	assert.Contains(t, stderr, "accepts 1 arg(s), received 0")
}
