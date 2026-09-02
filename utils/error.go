package utils

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	AuthMFARequired  = "auth_mfa_required"
	UsernameRequired = "user_username_required"

	// ServerBusyWithUserWork: disruptive action refused; --force overrides it.
	ServerBusyWithUserWork = "server_busy_with_user_work"

	// CommandInlineCredential: alpacon-server refused a command because its command
	// line carries a credential (e.g. a -p/--password flag, a KEY=VALUE secret such
	// as PGPASSWORD=..., or a user:pass@host connection string), which the server
	// would otherwise persist in the stored command line. Move the secret to
	// --env instead. See alpacon-server ADR 0037 (Refs alpacax/alpacon-server#2745).
	// The rejected-forms list above is repeated in the exec and websh help text
	// and the README's "When a command is denied" section—when the server-side
	// gate changes, update all of them together.
	//
	// The server also accepts a per-command credential_exposure_acknowledged
	// override, which the CLI deliberately does not expose: it records the
	// exposure rather than preventing it (hygiene, not authorization—it carries
	// no role gate), so exposing it would offer a one-flag way past the gate
	// where rewriting with --env costs the same. --env stays the only path.
	CommandInlineCredential = "command_inline_credential"

	// APITokenACLNotAllowed: alpacon-server refused the request because the token's
	// access control rules do not cover it—the command, the server, or the file path
	// falls outside the envelope an admin granted this token. The refusal is
	// permanent for the same request: a retry submits the same thing. Widen the
	// rules with 'alpacon token acl' instead. The server sends this on a 403 with no
	// human detail, so authStatusCodeMessage in client/client.go renders the message
	// (Refs alpacax/alpacon-server#2804).
	APITokenACLNotAllowed = "api_token_acl_not_allowed"

	// WorkSession gate codes (returned by alpacon-server work_sessions/services.py)
	WorkSessionRequired         = "work_session_required"
	WorkSessionNotUsable        = "work_session_not_usable"
	WorkSessionNotActive        = "work_session_not_active"
	WorkSessionExpired          = "work_session_expired"
	WorkSessionScopeNotAllowed  = "work_session_scope_not_allowed"
	WorkSessionServerNotAllowed = "work_session_server_not_allowed"
	WorkSessionAssigneeMismatch = "work_session_assignee_mismatch"

	// ExitCodeGeneralError is the process exit code for an ordinary failure—what
	// CliErrorWithExit already exits with. Name it when calling CliErrorWithExitCode
	// so the general case reads like the specific ones beside it.
	ExitCodeGeneralError = 1

	// ExitCodeUsageError is the process exit code for a flag or argument a command
	// rejected in its own validation. It is distinct from ExitCodeGeneralError (1):
	// 1 means the request failed (network, server), 2 means the invocation itself
	// was wrong. Scripts and AI agents branch on it to fix the command line rather
	// than retry unchanged.
	ExitCodeUsageError = 2

	// ExitCodeWorkSessionDenied is the process exit code for WorkSession gate refusals.
	ExitCodeWorkSessionDenied = 3

	// ExitCodePendingApproval is the process exit code for an action whose
	// approval is still open: a sudo HITL denial that created an approval
	// request, a work session created in the pending state, or a wait that ended
	// (timed out or was interrupted) with the outcome undecided.
	// It is distinct from ExitCodeWorkSessionDenied (3): the action was not
	// refused, it is awaiting an out-of-band approve/reject in the Alpacon console
	// (web/Slack). Scripts and AI agents branch on it to "wait or check later"
	// rather than treat it as a hard failure.
	ExitCodePendingApproval = 4

	// PendingApprovalStatus is the stable machine-readable status string emitted
	// under --output json when an action is pending human approval.
	PendingApprovalStatus = "pending_approval"

	// ExitCodeServerBusy is the process exit code for a disruptive server action
	// refused because the server has active user work (server_busy_with_user_work).
	// It is a transient, retryable "busy now → retry later" condition, distinct from
	// a hard failure (1): scripts and AI agents branch on it to retry when idle
	// (or re-run with --force) rather than give up.
	ExitCodeServerBusy = 5

	// ExitCodeNotApproved is the process exit code for an awaited approval that
	// ended without being granted—rejected, expired, revoked, cancelled, or
	// completed. It is the counterpart of ExitCodePendingApproval (4): 4 means the
	// outcome is still open, 6 means it settled without the grant. Scripts and AI
	// agents branch on it to stop retrying rather than keep re-requesting approval.
	ExitCodeNotApproved = 6

	// ExitCodePurposeRequired is the process exit code for a command the
	// verification gate parked while it asks what the command is for (ADR 0052).
	// It is deliberately not ExitCodePendingApproval (4): nothing is pending on a
	// human, no approval request exists, and the next move belongs to the caller
	// that submitted the command. Scripts and AI agents branch on it to answer
	// with 'alpacon exec purpose' rather than to wait or to give up—and the
	// window is about a minute, so waiting loses it.
	ExitCodePurposeRequired = 7

	// PurposeRequiredStatus is the stable machine-readable status string emitted
	// under --output json when a command is parked awaiting its purpose.
	PurposeRequiredStatus = "purpose_required"

	// ExitCodeUpdateAvailable is the process exit code for `alpacon update --check`
	// finding a newer release. It is distinct from ExitCodeGeneralError (1): 1 means
	// the check itself failed, 8 means the check succeeded and a newer version
	// exists. Scripts and CI branch on it to schedule an update.
	ExitCodeUpdateAvailable = 8
)

type ErrorResponse struct {
	Code   string `json:"code"`
	Source string `json:"source"`
}

type codedError interface {
	ErrorCode() string
	ErrorSource() string
}

type statusCoder interface {
	HTTPStatusCode() int
}

type retryAfterCarrier interface {
	RetryAfter() time.Duration
}

// HTTPStatusCode returns the HTTP status carried by err, or 0 if none—lets callers tell 404 from 401.
func HTTPStatusCode(err error) int {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if sc, ok := e.(statusCoder); ok {
			return sc.HTTPStatusCode()
		}
	}
	return 0
}

// RetryAfter returns the delay the server asked for on err, or 0 if it sent none.
func RetryAfter(err error) time.Duration {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if ra, ok := e.(retryAfterCarrier); ok {
			return ra.RetryAfter()
		}
	}
	return 0
}

// IsRetryLaterStatus reports whether a 4xx is asking to be retried rather than refusing.
func IsRetryLaterStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests
}

// IsFatalClientError reports whether a 4xx will fail the same way on retry.
func IsFatalClientError(status int) bool {
	if IsRetryLaterStatus(status) {
		return false
	}
	return status >= http.StatusBadRequest && status < http.StatusInternalServerError
}

// IsTransientRequestError reports whether err may not repeat on the next
// attempt. A missing status covers a request that never reached the server and
// a response that failed to decode, so a caller must bound its retries.
func IsTransientRequestError(err error) bool {
	if err == nil {
		return false
	}
	status := HTTPStatusCode(err)
	if status == 0 {
		return true
	}
	return !IsFatalClientError(status)
}

func ParseErrorResponse(err error) (string, string) {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if coded, ok := e.(codedError); ok {
			code, source := coded.ErrorCode(), coded.ErrorSource()
			if code != "" || source != "" {
				return code, source
			}
		}

		errStr := e.Error()

		// Try JSON format: {"code": "...", "source": "..."}
		start := strings.Index(errStr, "{")
		if start != -1 {
			var errorResp ErrorResponse
			if jsonErr := json.Unmarshal([]byte(errStr[start:]), &errorResp); jsonErr == nil && (errorResp.Code != "" || errorResp.Source != "") {
				return errorResp.Code, errorResp.Source
			}
		}

		// Try "code: X; source: Y" format (produced by parseAPIError in the HTTP client)
		var iterCode, iterSource string
		for part := range strings.SplitSeq(errStr, "; ") {
			part = strings.TrimSpace(part)
			if after, ok := strings.CutPrefix(part, "code: "); ok {
				iterCode = after
			} else if after, ok := strings.CutPrefix(part, "source: "); ok {
				iterSource = after
			}
		}
		if iterCode != "" {
			return iterCode, iterSource
		}
	}

	return "", ""
}
