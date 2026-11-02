package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/localsignal"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type signalRefusalStub struct {
	code, gate string
	status     int
	payload    map[string]any
}

func (e signalRefusalStub) HTTPStatusCode() int {
	if e.status != 0 {
		return e.status
	}
	return 403
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
		{"file ACL with token code", &signalRefusalStub{code: "api_token_acl_not_allowed", gate: "file_acl"}, localsignal.Denied, "ses-1"},
		{"Work Session fallback", &signalRefusalStub{code: WorkSessionRequired}, localsignal.Denied, "ses-1"},
		{"unknown gate", &signalRefusalStub{gate: "future_gate"}, "", ""},
		{"network gate", &signalRefusalStub{gate: "network"}, "", ""},
		{"gate refusal", &signalRefusalStub{code: "refused", gate: "risk"}, localsignal.Denied, "ses-1"},
		{"paused wins over gate", &signalRefusalStub{code: "session_paused", gate: "session"}, "", ""},
		{"paused without gate", &signalRefusalStub{code: "session_paused"}, "", ""},
		{"response session", &signalRefusalStub{gate: "risk", payload: map[string]any{"work_session_id": "ses-2"}}, localsignal.Denied, "ses-2"},
		{"response object", &signalRefusalStub{gate: "risk", payload: map[string]any{"work_session": map[string]any{"id": "ses-2"}}}, localsignal.Denied, "ses-2"},
		{"batch refusal", &BatchError{Failures: []error{&signalRefusalStub{gate: "risk"}}}, localsignal.Denied, "ses-1"},
		{"batch pause", &BatchError{Failures: []error{&signalRefusalStub{code: "session_paused"}}}, "", ""},
		{"plan", &signalRefusalStub{status: http.StatusPaymentRequired, gate: "risk"}, "", ""},
		{"rate", &signalRefusalStub{status: http.StatusTooManyRequests, gate: "risk"}, "", ""},
		{"validation", &signalRefusalStub{status: http.StatusBadRequest, gate: "risk"}, "", ""},
		{"token scope", &signalRefusalStub{code: "api_token_scope_missing", gate: "token_scope"}, "", ""},
		{"role", &signalRefusalStub{code: "rbac_permission_required", gate: "role"}, "", ""},
		{"presence", &signalRefusalStub{code: "auth_mfa_required", gate: "presence"}, "", ""},
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
	err := &signalRefusalStub{code: "session_paused", gate: "session"}
	_, stderr := testutil.CaptureOutput(t, func() { CliError("command failed: %s", err) })
	assert.Contains(t, stderr, "banner paused\nError: command failed: refused")
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
		{"gate without code", 403, `{"gate":"risk"}`, ""},
		{"gated", 403, `{"code":"refused","gate":"risk"}`, ""},
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

func TestSignalBatchDeduplicatesRefusals(t *testing.T) {
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
	assert.Equal(t, 1, calls)
}

func TestGenericErrorSinksDoNotSignalDenied(t *testing.T) {
	old := deliverLocalSignal
	t.Cleanup(func() { deliverLocalSignal = old })
	calls := 0
	deliverLocalSignal = func(localsignal.Kind, localsignal.Fields) { calls++ }
	_, _ = testutil.CaptureOutput(t, func() {
		CliError("failed: %s", &signalRefusalStub{gate: "risk"})
		SignalResponse(403, []byte(`{"code":"refused","gate":"risk"}`))
	})
	assert.Zero(t, calls)
}

func TestBatchSignalKindsAndSessions(t *testing.T) {
	old := deliverLocalSignal
	t.Cleanup(func() { deliverLocalSignal = old })
	var got []string
	deliverLocalSignal = func(k localsignal.Kind, f localsignal.Fields) {
		got = append(got, f.Session+":"+string(k))
		if f.Session == "ses-2" {
			assert.Equal(t, "https://console.example/session/2", f.ConsoleURL)
		}
	}
	SignalRefusal(&BatchError{Failures: []error{
		&signalRefusalStub{gate: "risk"}, &signalRefusalStub{gate: "risk"},
		WithSignalFields(&signalRefusalStub{gate: "risk"}, localsignal.Fields{Session: "ses-3"}),
		WithSignalFields(&signalRefusalStub{gate: "risk"}, localsignal.Fields{Session: "ses-4"}),
		&signalRefusalStub{code: "session_paused"}, &signalRefusalStub{code: "session_paused"},
		&signalRefusalStub{gate: "risk", payload: map[string]any{"work_session_id": "ses-2", "console_url": "https://console.example/session/2"}},
	}})
	assert.Equal(t, []string{":denied", "ses-3:denied", "ses-4:denied", "ses-2:denied"}, got)
}

type failingSignalNotifier struct{ mode, marker string }

func (n failingSignalNotifier) Notify(ctx context.Context, _, _, _ string) error {
	if err := os.WriteFile(n.marker, []byte("stub invoked"), 0600); err != nil {
		return err
	}
	if n.mode == "hang" {
		<-ctx.Done()
		return ctx.Err()
	}
	return errors.New("notification failure")
}

func TestSignalFailOpenHelper(t *testing.T) {
	if os.Getenv("GO_WANT_SIGNAL_RESULT_HELPER") != "1" {
		return
	}
	mode := os.Getenv("SIGNAL_TEST_MODE")
	if mode == "baseline" {
		deliverLocalSignal = func(localsignal.Kind, localsignal.Fields) {}
	}
	localsignal.NewNotifier = func() localsignal.Notifier { return failingSignalNotifier{mode, os.Getenv("SIGNAL_TEST_MARKER")} }
	localsignal.OpenTTY = func() (io.WriteCloser, error) { return nil, errors.New("no controlling terminal") }
	OutputFormat = os.Getenv("SIGNAL_TEST_FORMAT")
	_, _ = fmt.Fprint(os.Stdout, "command output\n")
	err := &signalRefusalStub{code: "session_paused", gate: "session"}
	CliErrorEnvelopeWithExit("command", err, "command failed: %s", err)
}

func TestSignalFailurePreservesCommandResult(t *testing.T) {
	for _, format := range []string{OutputFormatJSON, OutputFormatTable} {
		t.Run(format, func(t *testing.T) {
			run := func(t *testing.T, mode string) (string, string, int) {
				t.Helper()
				cmd := exec.Command(os.Args[0], "-test.run=^TestSignalFailOpenHelper$")
				marker := filepath.Join(t.TempDir(), "notification")
				var env []string
				for _, value := range os.Environ() {
					key, _, _ := strings.Cut(value, "=")
					if key != "CI" && key != "SSH_CONNECTION" && key != "SSH_TTY" {
						env = append(env, value)
					}
				}
				home := t.TempDir()
				cmd.Env = append(env, "HOME="+home, "USERPROFILE="+home, "GO_WANT_SIGNAL_RESULT_HELPER=1", "SIGNAL_TEST_MODE="+mode, "SIGNAL_TEST_FORMAT="+format, "SIGNAL_TEST_MARKER="+marker, "DISPLAY=:0", "DBUS_SESSION_BUS_ADDRESS=test")
				var stdout, stderr bytes.Buffer
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				err := cmd.Run()
				var exit *exec.ExitError
				require.ErrorAs(t, err, &exit)
				if mode != "baseline" {
					assert.FileExists(t, marker)
				} else {
					assert.NoFileExists(t, marker)
				}
				text := stderr.String()
				if mode != "baseline" && format != OutputFormatJSON {
					_, body := localsignal.Format(localsignal.Paused, localsignal.Fields{}, 0)
					banner := "==== ALPACON: SESSION PAUSED ====\n" + body + "\n================================\n"
					require.Contains(t, text, banner)
					text = strings.Replace(text, banner, "", 1)
				}
				return stdout.String(), text, exit.ExitCode()
			}
			wantOut, wantErr, wantExit := run(t, "baseline")
			for _, mode := range []string{"error", "hang"} {
				t.Run(mode, func(t *testing.T) {
					out, err, code := run(t, mode)
					assert.Equal(t, wantOut, out)
					assert.Equal(t, wantErr, err)
					assert.Equal(t, wantExit, code)
				})
			}
		})
	}
}

func TestCommandDenialAllowlist(t *testing.T) {
	t.Parallel()
	for _, gate := range []string{"server_reach", "command_preapproval", "command_acl", "file_acl", "risk", "approval", "sudo", "work_session", "block"} {
		t.Run(gate, func(t *testing.T) {
			t.Parallel()
			assert.True(t, commandDenial("api_token_acl_not_allowed", gate), "gate %s must signal a command refusal", gate)
		})
	}
	for _, gate := range []string{"", "plan", "rate", "token_scope", "role", "presence", "network", "future_gate"} {
		name := gate
		if name == "" {
			name = "missing"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.False(t, commandDenial("refused", gate), "gate %s must not signal a command refusal", gate)
			assert.False(t, commandDenial(WorkSessionRequired, gate+"_unknown"), "unknown gate must not fall back to a Work Session code")
		})
	}
	assert.False(t, commandDenial("refused", "risk,approval"), "a combined gate must not match the allowlist")
	assert.False(t, commandDenial("refused", "risk|approval"), "a combined gate must not match the allowlist")
	assert.True(t, commandDenial(WorkSessionRequired, ""), "a Work Session code without a gate must signal a refusal")
}

func TestCommandPauseThenSinkDoesNotFold(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	err := WithSignalFields(&signalRefusalStub{code: "session_paused"}, localsignal.Fields{Session: "ses-1"})
	_, _ = testutil.CaptureOutput(t, func() {
		SignalRefusal(err)
		CliError("command failed: %s", err)
	})
	data, readErr := os.ReadFile(filepath.Join(home, ".alpacon", "state", "signals.json"))
	require.NoError(t, readErr)
	var state map[string]struct {
		Folded int `json:"folded"`
	}
	require.NoError(t, json.Unmarshal(data, &state))
	require.Contains(t, state, "paused:ses-1")
	assert.Zero(t, state["paused:ses-1"].Folded)
}
