package utils

import (
	"errors"
	"fmt"
	"strings"
	"testing"

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

func TestBuildCliErrorEnvelopeFromErr_CarriesTheServerHint(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("failed to grant role: %w", &hintedError{code: "rbac_permission_required", hint: sampleHint})
	rendered, fmtErr := FormatJSON(buildCliErrorEnvelopeFromErr("role grant", err, "Failed to grant role."))
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

func TestBuildCliErrorEnvelopeFromErr_NoHintNoNextActions(t *testing.T) {
	t.Parallel()
	env := buildCliErrorEnvelopeFromErr("role grant", &hintedError{code: "rbac_permission_required"}, "Failed to grant role.")

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
