package worksession

import (
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
)

func TestOperationScopes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		operation Operation
		want      []string
	}{
		{name: "command", operation: Command, want: []string{"command"}},
		{name: "file command", operation: FileCommand, want: []string{"command", "webftp"}},
		{name: "websh", operation: Websh, want: []string{"websh"}},
		{name: "transfer", operation: Transfer, want: []string{"webftp"}},
		{name: "tunnel", operation: Tunnel, want: []string{"tunnel"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, (Need{Operation: tt.operation}).scopes())
		})
	}
}

func TestFixedNeverAddsAnOffer(t *testing.T) {
	t.Parallel()
	callbacks := Fixed("session-1").Offer(utils.ErrorHandlerCallbacks{})
	assert.Nil(t, callbacks.OnWorkSessionRefused)
	assert.Equal(t, "session-1", Fixed("session-1").ID())
	assert.Empty(t, (*Selection)(nil).ID())
}

func TestResumeWithNamesTheCommandItsStepRunsAndKeepsTheResend(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		resume     func(*Selection)
		wantResume string
	}{
		{name: "no_resume_step", resume: func(*Selection) {}, wantResume: "edit"},
		{name: "step_that_keeps_the_command", resume: func(s *Selection) { s.ResumeWith("re-run this command without --upload") }, wantResume: "edit"},
		{name: "step_that_runs_another_command", resume: func(s *Selection) { s.ResumeWithCommand("upload the edited copy", "cp") }, wantResume: "cp"},
		{name: "later_step_that_keeps_the_command_drops_the_other", resume: func(s *Selection) {
			s.ResumeWithCommand("upload the edited copy", "cp")
			s.ResumeWith("re-run this command without --upload")
		}, wantResume: "edit"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			selection := &Selection{need: Need{Operation: Transfer, Command: "edit"}}

			tt.resume(selection)

			assert.Equal(t, tt.wantResume, selection.resumeCommand())
			assert.Equal(t, "edit", selection.need.command(), "the resend is still the command the user ran")
		})
	}
}
