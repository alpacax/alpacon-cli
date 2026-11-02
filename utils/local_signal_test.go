package utils

import (
	"errors"
	"fmt"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/localsignal"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type signalRefusalStub struct {
	code, gate string
	payload    map[string]any
}

func (e signalRefusalStub) Error() string               { return "refused" }
func (e signalRefusalStub) ErrorCode() string           { return e.code }
func (e signalRefusalStub) ErrorSource() string         { return "" }
func (e signalRefusalStub) ErrorGate() string           { return e.gate }
func (e signalRefusalStub) ErrorMissing() []string      { return nil }
func (e signalRefusalStub) ErrorFields() map[string]any { return e.payload }

func TestSignalRefusalClassification(t *testing.T) {
	old := deliverLocalSignal
	t.Cleanup(func() { deliverLocalSignal = old })
	for _, tc := range []struct {
		name    string
		err     error
		kind    localsignal.Kind
		session string
	}{
		{"gate refusal", &signalRefusalStub{code: "refused", gate: "risk"}, localsignal.Denied, "ses-1"},
		{"paused wins over gate", &signalRefusalStub{code: "session_paused", gate: "session"}, localsignal.Paused, "ses-1"},
		{"paused without gate", &signalRefusalStub{code: "session_paused"}, localsignal.Paused, "ses-1"},
		{"response session", &signalRefusalStub{gate: "risk", payload: map[string]any{"work_session_id": "ses-2"}}, localsignal.Denied, "ses-2"},
		{"response object", &signalRefusalStub{gate: "risk", payload: map[string]any{"work_session": map[string]any{"id": "ses-2"}}}, localsignal.Denied, "ses-2"},
		{"batch refusal", &BatchError{Failures: []error{&signalRefusalStub{gate: "risk"}}}, localsignal.Denied, "ses-1"},
		{"batch pause", &BatchError{Failures: []error{&signalRefusalStub{code: "session_paused"}}}, localsignal.Paused, "ses-1"},
		{"auth", &signalRefusalStub{code: AuthAuthenticationFailed}, "", ""},
		{"input", MarkUsageError(errors.New("invalid flag")), "", ""},
		{"network", errors.New("connection refused"), "", ""},
		{"message is not a code", errors.New(`session_paused {"gate":"risk"}`), "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			deliverLocalSignal = func(kind localsignal.Kind, fields localsignal.Fields) {
				calls++
				assert.Equal(t, tc.kind, kind)
				assert.Equal(t, tc.session, fields.Session)
				assert.Equal(t, "sudo", fields.Argv0)
				assert.Equal(t, "my-server", fields.Server)
				assert.Equal(t, "alice", fields.Account)
			}
			err := WithSignalFields(fmt.Errorf("command failed: %w", tc.err), localsignal.Fields{Argv0: "sudo", Server: "my-server", Account: "alice", Session: "ses-1"})
			SignalRefusal(err)
			if tc.kind == "" {
				assert.Zero(t, calls)
			} else {
				assert.Equal(t, 1, calls)
			}
			require.ErrorIs(t, err, tc.err)
		})
	}
}

func TestSignalBeforeErrorAndSinglePendingAnnouncement(t *testing.T) {
	old := deliverLocalSignal
	t.Cleanup(func() { deliverLocalSignal = old })
	calls := 0
	deliverLocalSignal = func(kind localsignal.Kind, _ localsignal.Fields) {
		calls++
		fmt.Fprintf(TrackedStderr(), "banner %s\n", kind)
	}
	err := &signalRefusalStub{code: "refused", gate: "risk"}
	_, stderr := testutil.CaptureOutput(t, func() { CliError("command failed: %s", err) })
	assert.Contains(t, stderr, "banner denied\nError: command failed: refused")
	assert.Equal(t, 1, calls)
	calls = 0
	SignalApprovalPending(err, localsignal.Fields{Session: "ses-1"})
	SignalApprovalPending(MarkApprovalSignaled(err), localsignal.Fields{Session: "ses-1"})
	assert.Equal(t, 1, calls)
}

func TestSignalRawResponse(t *testing.T) {
	old := deliverLocalSignal
	t.Cleanup(func() { deliverLocalSignal = old })
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   localsignal.Kind
	}{
		{"gate without code", 403, `{"gate":"risk"}`, localsignal.Denied},
		{"gated", 403, `{"code":"refused","gate":"risk"}`, localsignal.Denied},
		{"paused", 403, `{"code":"session_paused","work_session_id":"ses-1"}`, localsignal.Paused},
		{"validation", 400, `{"code":"invalid","gate":"a request field"}`, ""},
		{"authentication", 401, `{"code":"auth_authentication_failed"}`, ""},
		{"invalid json", 403, `gateway error`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var kinds []localsignal.Kind
			deliverLocalSignal = func(kind localsignal.Kind, _ localsignal.Fields) { kinds = append(kinds, kind) }
			SignalResponse(tc.status, []byte(tc.body))
			if tc.want == "" {
				assert.Empty(t, kinds)
			} else {
				assert.Equal(t, []localsignal.Kind{tc.want}, kinds)
			}
		})
	}
}

func TestSignalBatchCountsEachRefusal(t *testing.T) {
	old := deliverLocalSignal
	t.Cleanup(func() { deliverLocalSignal = old })
	calls := 0
	deliverLocalSignal = func(kind localsignal.Kind, _ localsignal.Fields) {
		calls++
		assert.Equal(t, localsignal.Denied, kind)
	}
	SignalRefusal(&BatchError{Failures: []error{
		&signalRefusalStub{gate: "risk"}, &signalRefusalStub{gate: "risk"},
	}})
	assert.Equal(t, 2, calls)
}
