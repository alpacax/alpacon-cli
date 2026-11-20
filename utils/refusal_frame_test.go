package utils

import (
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/localsignal"
	"github.com/stretchr/testify/assert"
)

func TestPausedRefusalFrame(t *testing.T) {
	t.Parallel()
	const frame = `{"type":"refusal","code":"session_paused","gate":"work_session","missing":"work_session",` +
		`"work_session_id":"11111111-1111-4111-8111-111111111111",` +
		`"console_url":"https://alpacon.io/acme/work-sessions/11111111-1111-4111-8111-111111111111",` +
		`"next":"/api/work-sessions/sessions/11111111-1111-4111-8111-111111111111/","hint":"This work session is paused."}`
	for _, tc := range []struct {
		name   string
		frame  string
		fields localsignal.Fields
		ok     bool
	}{
		{"paused refusal", frame, localsignal.Fields{
			Session:    "11111111-1111-4111-8111-111111111111",
			ConsoleURL: "https://alpacon.io/acme/work-sessions/11111111-1111-4111-8111-111111111111",
		}, true},
		{"another refusal code", `{"type":"refusal","code":"something_else","work_session_id":"s"}`, localsignal.Fields{}, false},
		{"another frame type", `{"type":"idle_warning","code":"session_paused"}`, localsignal.Fields{}, false},
		{"not json", `session_paused`, localsignal.Fields{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fields, ok := PausedRefusalFrame([]byte(tc.frame))
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.fields, fields)
		})
	}
}
