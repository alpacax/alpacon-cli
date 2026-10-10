package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	AuthMFARequired  = "auth_mfa_required"
	UsernameRequired = "user_username_required"

	// Codes the server sends on its own 401s.
	AuthTokenMissing         = "auth_token_missing"
	AuthAuthenticationFailed = "auth_authentication_failed"

	// AuthVerificationUnavailable rides a 503: the server could not check the
	// credential, did not reject it, and a retry can succeed.
	AuthVerificationUnavailable = "auth_verification_unavailable"

	// WorkspaceSudoWithMFAStepUpUnavailable rides a 403: the server refused a
	// change to the workspace's sudo-with-MFA setting because this deployment has
	// no MFA sign-in. Retrying or completing MFA cannot lift it.
	WorkspaceSudoWithMFAStepUpUnavailable = "workspace_sudo_with_mfa_step_up_unavailable"

	// WorkspaceExecutionControlConsoleOnly rides a 403: the edit loosens
	// execution control, which the server accepts from the web console only.
	WorkspaceExecutionControlConsoleOnly = "workspace_execution_control_console_only"

	// ApprovalPolicyConsoleOnly rides a 403: approval policies are written from
	// the web console only.
	ApprovalPolicyConsoleOnly = "approval_policy_console_only"

	// SudoVerifyCredentialCannotProveMFA rides a 403 on sudo grant verification:
	// the credential is an API token, a service token or a non-interactive token,
	// so it cannot complete sudo MFA. Signing in interactively can.
	SudoVerifyCredentialCannotProveMFA = "sudo_verify_credential_cannot_prove_mfa"

	// Codes the server sends on its own 404/429/406/415 refusals, with no detail.
	APINotFound             = "api_not_found"
	APIRateLimited          = "api_rate_limited"
	APINotAcceptable        = "api_not_acceptable"
	APIUnsupportedMediaType = "api_unsupported_media_type"

	// Codes the Elasticsearch cursor paginator sends when a walk cannot go on: an
	// expired snapshot (400), a mismatched cursor (400), or no snapshot opened (503).
	APICursorExpired     = "api_cursor_expired"
	APIInvalidCursor     = "api_invalid_cursor"
	APISearchUnavailable = "api_search_unavailable"

	// ServerBusyWithUserWork: disruptive action refused; --force overrides it.
	ServerBusyWithUserWork = "server_busy_with_user_work"

	// CommandInlineCredential: the server refused a command because its command
	// line carries a credential (e.g. a -p/--password flag, a KEY=VALUE secret such
	// as PGPASSWORD=..., or a user:pass@host connection string), which the server
	// would otherwise persist in the stored command line. Move the secret to
	// --env instead.
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

	// APITokenACLNotAllowed: the server refused the request because the token's
	// access control rules do not cover it—the command, the server, or the file path
	// falls outside the envelope an admin granted this token. The refusal is
	// permanent for the same request: a retry submits the same thing. Widen the
	// rules with 'alpacon token acl' instead. The server sends this on a 403 with no
	// human detail, so authStatusCodeMessage in client/client.go renders the message.
	APITokenACLNotAllowed = "api_token_acl_not_allowed"

	// The token-scope gate's refusals: the token's scopes do not cover this call, or
	// the server could not resolve the call's action to a scope at all.
	APITokenScopeMissing          = "api_token_scope_missing"
	APITokenScopeActionUnresolved = "api_token_scope_action_unresolved"

	// WorkSession gate codes the server returns
	WorkSessionRequired         = "work_session_required"
	WorkSessionNotUsable        = "work_session_not_usable"
	WorkSessionNotActive        = "work_session_not_active"
	WorkSessionExpired          = "work_session_expired"
	WorkSessionScopeNotAllowed  = "work_session_scope_not_allowed"
	WorkSessionServerNotAllowed = "work_session_server_not_allowed"
	WorkSessionAssigneeMismatch = "work_session_assignee_mismatch"

	// WorkSession extension approval. Every 'work-session extend' request
	// requires a reason and follows the workspace's approval policy, the same
	// rule 'work-session create' uses; a session may hold only one pending
	// extension request at a time. See cmd/worksession's extend command.
	WorkSessionExtensionReasonRequired = "work_session_extension_reason_required"
	WorkSessionExtensionAlreadyPending = "work_session_extension_already_pending"
	// WorkSessionAdmissionDenied: the workspace refuses this request outright
	// (session creation, or an extension)—not a queued request waiting on a
	// human, a hard no.
	WorkSessionAdmissionDenied = "work_session_admission_denied"

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
	// verification gate parked while it asks what the command is for.
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

// gateGuidanceError is the optional pair a codedError may also carry: which
// gate refused the request and what it was missing. A coded 402/403/405/429
// sends no human "detail" on these routes, so a caller that wants to say more
// than the generic fallback message reads these instead.
type gateGuidanceError interface {
	ErrorGate() string
	ErrorMissing() []string
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

// submissionError tags an error as the response or dial failure of the one
// request that carries an operation's side effect. See MarkSubmission.
type submissionError struct{ err error }

func (e *submissionError) Error() string { return e.err.Error() }
func (e *submissionError) Unwrap() error { return e.err }

// MarkSubmission tags err, if any, as the error of the request that carries the
// operation's side effect itself (the command submission, an upload or download
// create, a session create, a one-request change), including whatever lookup
// precedes that request. Only such an error says the server did or did not act,
// so only it can be retried after MFA or ridden through when it is unprocessed.
//
// The tag is read off the error exactly as returned, never through wrapping: an
// operation that makes further requests after the one it tagged returns their
// errors untagged, and a caller that wraps a tagged error hides the tag. An
// untagged error is therefore the safe default, "the operation already took
// effect": HandleCommonErrors passes it through and the wait ends on it. The
// status and dial error underneath stay readable through the tag.
func MarkSubmission(err error) error {
	if err == nil || IsSubmissionError(err) {
		return err
	}
	return &submissionError{err: err}
}

// IsSubmissionError reports whether err, as returned and not through any
// wrapping, is the tagged error of an operation's side-effecting request.
func IsSubmissionError(err error) bool {
	_, ok := err.(*submissionError)
	return ok
}

// endSubmission drops the tag HandleCommonErrors consumed, so an error leaving it
// cannot make an enclosing retry loop take a later result for a refused request.
func endSubmission(err error) error {
	if tagged, ok := err.(*submissionError); ok {
		return tagged.err
	}
	return err
}

// IsUnprocessedRequestError reports whether err shows the server did not act on
// the request: the connection never opened, or the server answered 429 or 503.
// Unlike IsTransientRequestError it leaves out a lost response, a 502 and a 504,
// which can follow a request that already ran, so a request that is not
// idempotent may be sent again on it. Only the tagged error of the
// side-effecting request itself (MarkSubmission) can be unprocessed: any other
// error came from a later request of an operation the server already acted on,
// whatever status it carries.
func IsUnprocessedRequestError(err error) bool {
	if err == nil || !IsSubmissionError(err) {
		return false
	}
	switch HTTPStatusCode(err) {
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return true
	case 0:
		var opErr *net.OpError
		return errors.As(err, &opErr) && opErr.Op == "dial"
	default:
		return false
	}
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

// RetryAfterFromHeader reads a Retry-After header as delta-seconds, the form a
// throttled response sends. An HTTP-date, a non-positive or an unrepresentable
// value reads as no hint, since misreading one would stall a wait.
func RetryAfterFromHeader(header http.Header) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(header.Get("Retry-After")))
	// The upper bound is what a time.Duration can hold: past it the
	// multiplication wraps, and a wrapped delay is worse than no hint at all.
	if err != nil || seconds <= 0 || int64(seconds) > math.MaxInt64/int64(time.Second) {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// StructuredErrorCode returns the code a typed error in err's chain carries, or
// "" if none does. Unlike ParseErrorResponse it never reads a code out of
// message text, so a code that only appears inside a message, such as an
// agent's transfer report, does not count.
func StructuredErrorCode(err error) string {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if coded, ok := e.(codedError); ok {
			if code := coded.ErrorCode(); code != "" {
				return code
			}
		}
	}
	return ""
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

// InteractiveOnly rewrites a token's refusal of an action only an interactive login may
// take; a scope refusal counts, since granting the scope only reaches the ACL refusal.
func InteractiveOnly(err error, action string) error {
	switch code, _ := ParseErrorResponse(err); code {
	case APITokenACLNotAllowed, APITokenScopeMissing:
		return fmt.Errorf("a token cannot %s; run 'alpacon login' without -t and try again", action)
	}
	return err
}

// ParseErrorGateAndMissing returns the "gate" and "missing" fields a coded
// 402/403/405/429 carried, or ("", nil) if err carries none. Unlike
// ParseErrorResponse this has no textual fallback: every producer of these
// fields is a *apiError in client/client.go, which always implements
// gateGuidanceError, so there is no legacy string form to recover one from.
func ParseErrorGateAndMissing(err error) (string, []string) {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if gg, ok := e.(gateGuidanceError); ok {
			gate, missing := gg.ErrorGate(), gg.ErrorMissing()
			if gate != "" || len(missing) > 0 {
				return gate, missing
			}
		}
	}
	return "", nil
}
