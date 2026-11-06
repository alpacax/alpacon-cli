package utils

import (
	"bytes"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithExitError_SilencesCobraOnlyForAnExitError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		err         error
		wantSilence bool
	}{
		{name: "exit error", err: &ExitError{Code: ExitCodeGeneralError}, wantSilence: true},
		{name: "plain error", err: errors.New("unreported"), wantSilence: false},
		{name: "success", err: nil, wantSilence: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cmd := &cobra.Command{}
			runE := WithExitError(func(*cobra.Command, []string) error { return tc.err })

			err := runE(cmd, nil)

			assert.Equal(t, tc.err, err)
			assert.Equal(t, tc.wantSilence, cmd.SilenceErrors)
			assert.Equal(t, tc.wantSilence, cmd.SilenceUsage)
		})
	}
}

func TestFprintExitError_PrintsTheFailureItsConstructorNames(t *testing.T) {
	t.Parallel()
	usageCause := MarkUsageError(errors.New("bad flag"))
	cases := []struct {
		name       string
		err        error
		wantCode   int
		wantLine   string
		wantFooter bool
	}{
		{name: "general error keeps the footer", err: NewExitError("lookup failed: %s", errors.New("boom")), wantCode: ExitCodeGeneralError, wantLine: "lookup failed: boom\n", wantFooter: true},
		{name: "chosen code drops the footer", err: NewExitErrorWithCode(ExitCodeServerBusy, "server busy"), wantCode: ExitCodeServerBusy, wantLine: "server busy\n"},
		{name: "usage error", err: NewUsageExitError("expected one %s", "ENDPOINT"), wantCode: ExitCodeUsageError, wantLine: "expected one ENDPOINT\n"},
		{name: "usage cause promotes exit one to two", err: NewExitError("%s", usageCause), wantCode: ExitCodeUsageError, wantLine: "bad flag\n"},
		{name: "code alone prints nothing", err: &ExitError{Code: ExitCodeGeneralError}, wantCode: ExitCodeGeneralError},
		{name: "envelope error keeps the footer", err: NewEnvelopeExitError("wait", errors.New("boom"), "subscribe failed."), wantCode: ExitCodeGeneralError, wantLine: "subscribe failed.\n", wantFooter: true},
		{name: "envelope usage cause promotes exit one to two", err: NewEnvelopeExitError("wait", usageCause, "bad target."), wantCode: ExitCodeUsageError, wantLine: "bad target.\n"},
		{name: "envelope usage error", err: NewUsageEnvelopeExitError("wait", "--type is required."), wantCode: ExitCodeUsageError, wantLine: "--type is required.\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			exitErr, _ := errors.AsType[*ExitError](tc.err)

			FprintExitError(&out, exitErr)

			assert.Equal(t, tc.wantCode, exitErr.Code)
			if tc.wantLine == "" {
				assert.Empty(t, out.String())
				return
			}
			assert.Contains(t, out.String(), tc.wantLine)
			if tc.wantFooter {
				assert.Contains(t, out.String(), gitIssueURL)
			} else {
				assert.NotContains(t, out.String(), gitIssueURL)
			}
		})
	}
}

func TestExitError_UnwrapsTheCauseAmongItsArguments(t *testing.T) {
	t.Parallel()
	cause := errors.New("boom")

	err := NewExitError("lookup failed: %s", cause)

	require.ErrorIs(t, err, cause)
	assert.EqualError(t, err, "lookup failed: boom")
}

func TestExitError_UnwrapsAnEnvelopeCauseKeptOutOfTheMessage(t *testing.T) {
	t.Parallel()
	cause := errors.New("boom")

	err := NewEnvelopeExitError("wait", cause, "%s.", "boom")

	require.ErrorIs(t, err, cause)
	assert.EqualError(t, err, "boom.")
}

// Serial: it sets the process-wide OutputFormat.
func TestFprintExitError_EnvelopeClassifiesItsCauseUnderItsOperation(t *testing.T) {
	saved := OutputFormat
	OutputFormat = OutputFormatJSON
	t.Cleanup(func() { OutputFormat = saved })
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "cause outside the message", err: NewEnvelopeExitError("wait", ErrNotLoggedIn, "%s.", "not logged in"), want: `{"ok":false,"exit_code":1,"error_code":"not_logged_in","message":"not logged in.","context":{"operation":"wait"}}`},
		{name: "usage error", err: NewUsageEnvelopeExitError("watch", "--type is required."), want: `{"ok":false,"exit_code":2,"error_code":"usage_error","message":"--type is required.","context":{"operation":"watch"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			exitErr, _ := errors.AsType[*ExitError](tc.err)

			FprintExitError(&out, exitErr)

			assert.JSONEq(t, tc.want, out.String())
		})
	}
}
