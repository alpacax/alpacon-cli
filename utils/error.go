package utils

import (
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ErrInvalidID marks a local refusal of an ID that is unsafe as a URL path segment.
var ErrInvalidID = errors.New("invalid id")

const (
	AuthMFARequired  = "auth_mfa_required"
	UsernameRequired = "user_username_required"

	// Codes the server sends on its own 401s.
	AuthTokenMissing         = "auth_token_missing"
	AuthAuthenticationFailed = "auth_authentication_failed"

	// AuthTokenMFAMissing rides a 401: the token was issued without the
	// workspace's sign-in MFA, and only a new login, which runs it, can lift it.
	AuthTokenMFAMissing = "auth_token_mfa_missing"

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

	// SudoPolicyConsoleOnly rides a 403: sudo policies are created or
	// widened from the web console only.
	SudoPolicyConsoleOnly = "sudo_policy_console_only"

	// A work session can receive a session-authorized sudo policy without a
	// person's approval from the web console only; both names ride a 403.
	WorkSessionSudoBypassConsoleOnly            = "work_session_sudo_bypass_console_only"
	WorkSessionSessionAuthorizedSudoConsoleOnly = "work_session_session_authorized_sudo_console_only"

	// SudoVerifyCredentialCannotProveMFA rides a 403 on sudo grant verification:
	// the credential is an API token, a service token or a non-interactive token,
	// so it cannot complete sudo MFA. Signing in interactively can.
	SudoVerifyCredentialCannotProveMFA = "sudo_verify_credential_cannot_prove_mfa"

	// Refusals of a session-authorized sudo policy bound to an AI agent session, both
	// on a 403. NeedsPresence: the write would leave the policy on an agent
	// session without a person proving presence for that request (a fresh MFA
	// on an interactive login). NeedsApproval: a direct write of such a policy
	// is refused; it has to arrive as a modification of the session, which a
	// person approves.
	SudoPolicyBypassAgentSessionNeedsPresence            = "sudo_policy_bypass_agent_session_needs_presence"
	SudoPolicyBypassAgentSessionNeedsApproval            = "sudo_policy_bypass_agent_session_needs_approval"
	SudoPolicySessionAuthorizedAgentSessionNeedsPresence = "sudo_policy_session_authorized_agent_session_needs_presence"
	SudoPolicySessionAuthorizedAgentSessionNeedsApproval = "sudo_policy_session_authorized_agent_session_needs_approval"

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

	// ServerBusyWithUserWork means the server refused a disruptive action; --force overrides it.
	ServerBusyWithUserWork = "server_busy_with_user_work"

	// ServerPAMInstallUnsupported rides a 400: the host has no alpamon-pam
	// package path (macOS, Windows, or an unknown package manager).
	ServerPAMInstallUnsupported = "server_pam_install_unsupported"

	// ServerPAMInstallRequiresSudoWithMFA rides a 400: refused while "Use sudo with MFA"
	// is off, or always on a deployment that cannot complete an MFA step-up.
	ServerPAMInstallRequiresSudoWithMFA = "server_pam_install_requires_sudo_with_mfa"

	// ServerPAMInstallInProgress rides a 409: an alpamon-pam install is already pending.
	ServerPAMInstallInProgress = "server_pam_install_in_progress"

	// CommandInlineCredential means the server refused a command whose command line carries a
	// credential (a -p/--password flag, a KEY=VALUE secret such as PGPASSWORD=..., or a
	// user:pass@host connection string) it would otherwise store. Move the secret to --env. The
	// exec and websh help and the README's "When a command is denied" section repeat these forms;
	// update them together.
	//
	// The CLI deliberately does not expose the server's credential_exposure_acknowledged override:
	// it records the exposure rather than preventing it (hygiene, not authorization, with no role
	// gate), so it would be a one-flag way past the gate where rewriting with --env costs the same.
	// --env stays the only path.
	CommandInlineCredential = "command_inline_credential"

	// APITokenACLNotAllowed means the token's access control rules do not cover the command, server
	// or file path, so a retry fails the same way; widen them with 'alpacon token acl'. It rides a
	// 403 with no detail, so authStatusCodeMessage in client/client.go renders the message.
	APITokenACLNotAllowed = "api_token_acl_not_allowed"

	// CommandACLMoved rides a 405 on a command-ACL write once command ACLs live on
	// the command pre-approval ledger; codeOnlyMessage in client/client.go renders it.
	CommandACLMoved = "command_acl_moved"

	// AuthInteractiveChannelRequiresPresence rides a 403: a token gets no interactive channel
	// whatever its scopes and ACL rules say; only 'alpacon login' without -t lifts it.
	AuthInteractiveChannelRequiresPresence = "auth_interactive_channel_requires_presence"

	// AuthMFARequiresHumanPrincipal rides a 403: the action needs a person's MFA, which a token
	// (API or service) cannot complete; 'alpacon login' without -t can. Removing the action from
	// the workspace's MFA required actions also lifts it, except for actions the server always
	// holds to this (security settings, sudo and approval policy writes, RBAC roles).
	AuthMFARequiresHumanPrincipal = "auth_mfa_requires_human_principal"

	// The token-scope gate's refusals: the token's scopes do not cover this call, or
	// the server could not resolve the call's action to a scope at all.
	APITokenScopeMissing          = "api_token_scope_missing"
	APITokenScopeActionUnresolved = "api_token_scope_action_unresolved"

	// WorkSession gate codes the server returns.
	WorkSessionRequired         = "work_session_required"
	WorkSessionNotUsable        = "work_session_not_usable"
	WorkSessionNotActive        = "work_session_not_active"
	WorkSessionExpired          = "work_session_expired"
	WorkSessionScopeNotAllowed  = "work_session_scope_not_allowed"
	WorkSessionServerNotAllowed = "work_session_server_not_allowed"
	WorkSessionAssigneeMismatch = "work_session_assignee_mismatch"
	// WorkSessionAgentSessionRequired means the CLI declared an AI agent, and the
	// session it named is a user session. Only an agent session serves it.
	WorkSessionAgentSessionRequired = "work_session_agent_session_required"

	// WorkSessionAccountNotApproved means the command names an account other than
	// the one the session was approved to run as.
	WorkSessionAccountNotApproved = "work_session_account_not_approved"

	// Refusals of the account a work session create names with --account.
	WorkSessionTargetAccountAbsent       = "work_session_target_account_absent"
	WorkSessionTargetAccountNotReachable = "work_session_target_account_not_reachable"
	TargetAccountOtherPerson             = "target_account_other_person"

	// WorkSessionClusterNotAllowed: the cluster a kube session names is not
	// the one its work session targets.
	WorkSessionClusterNotAllowed = "work_session_cluster_not_allowed"

	// Refusals of a work session create's targets and lifetime. A session
	// targets servers or exactly one cluster, never both and never neither; a
	// cluster session holds kube and optionally kube_exec, nothing else, and
	// names no account; and its lifetime stays within the workspace's maximum.
	WorkSessionMixedTargets               = "work_session_mixed_targets"
	WorkSessionNoServers                  = "work_session_no_servers"
	WorkSessionTooManyClusters            = "work_session_too_many_clusters"
	WorkSessionInvalidScope               = "work_session_invalid_scope"
	ClusterNotFound                       = "cluster_not_found"
	WorkSessionTargetAccountNotApplicable = "work_session_target_account_not_applicable"
	WorkSessionExpiresExceedsMaxTTL       = "work_session_expires_exceeds_max_ttl"

	// Refusals of a kube session the server names itself. The cluster's agent
	// is not connected; the agent did not accept the session, which the server
	// then closed; and the caller has no user name the session could act as.
	ClusterNotConnected              = "cluster_not_connected"
	KubeSessionChannelOpenFailed     = "kube_session_channel_open_failed"
	KubeSessionPrincipalNotSupported = "kube_session_principal_not_supported"

	// WorkSession extension refusals: every 'work-session extend' needs a reason and follows the
	// workspace's approval policy, as 'work-session create' does, and a session holds at most one
	// pending extension request.
	WorkSessionExtensionReasonRequired = "work_session_extension_reason_required"
	WorkSessionExtensionAlreadyPending = "work_session_extension_already_pending"
	// WorkSessionAdmissionDenied means the workspace refuses this request outright
	// (session creation, or an extension)—not a queued request waiting on a
	// human, a hard no.
	WorkSessionAdmissionDenied = "work_session_admission_denied"

	// ExitCodeGeneralError is the process exit code for an ordinary failure, the one
	// CliErrorWithExit exits with. Name it when calling CliErrorWithExitCode so the general case
	// reads like the specific ones beside it.
	ExitCodeGeneralError = 1

	// ExitCodeUsageError is the process exit code for a flag or argument a command rejected, in
	// Cobra's parse or in its own validation. Unlike ExitCodeGeneralError, it means the invocation
	// itself was wrong, so scripts and AI agents fix the command line rather than retry it unchanged.
	ExitCodeUsageError = 2

	// ExitCodeWorkSessionDenied is the process exit code for WorkSession gate refusals.
	ExitCodeWorkSessionDenied = 3

	// ExitCodePendingApproval is the process exit code for an action whose approval is still open:
	// a sudo denial that created an approval request, a work session created pending, or a wait
	// that ended (timed out or interrupted) undecided. Unlike ExitCodeWorkSessionDenied nothing was
	// refused, so scripts and AI agents wait or check later.
	ExitCodePendingApproval = 4

	// PendingApprovalStatus is the stable machine-readable status string emitted
	// under --output json when an action is pending human approval.
	PendingApprovalStatus = "pending_approval"

	// ExitCodeServerBusy is the process exit code for a disruptive server action refused because
	// the server has active user work (server_busy_with_user_work). Scripts and AI agents retry
	// when the server is idle, or re-run with --force.
	ExitCodeServerBusy = 5

	// ExitCodeNotApproved is the process exit code for an awaited approval that ended without the
	// grant: rejected, expired, revoked, cancelled, or completed. Where ExitCodePendingApproval
	// means the outcome is still open, this one tells scripts and AI agents to stop re-requesting
	// approval.
	ExitCodeNotApproved = 6

	// ExitCodePurposeRequired is the process exit code for a command the verification gate parked
	// while it asks what the command is for. No approval request exists, so it is not
	// ExitCodePendingApproval: the caller answers with 'alpacon exec purpose' within about a minute
	// rather than wait.
	ExitCodePurposeRequired = 7

	// PurposeRequiredStatus is the stable machine-readable status string emitted
	// under --output json when a command is parked awaiting its purpose.
	PurposeRequiredStatus = "purpose_required"

	// ExitCodeUpdateAvailable is the process exit code for `alpacon update --check` finding a newer
	// release; a failed check exits ExitCodeGeneralError instead.
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

// HTTPStatusCode returns the HTTP status carried by err, or 0 if none. Along a chain the outermost
// status wins. An error that joins several (a BatchError, errors.Join, fmt.Errorf with more than
// one %w) has a status only when the statuses found across its tree, nested joins included, are
// all one; two or more distinct statuses give 0.
func HTTPStatusCode(err error) int {
	statuses := map[int]bool{}
	collectStatusCodes(err, statuses)
	if len(statuses) != 1 {
		return 0
	}
	for status := range statuses {
		return status
	}
	return 0
}

// collectStatusCodes adds the status each branch of err's tree names: the
// first status along a chain, and every branch of a join.
func collectStatusCodes(err error, statuses map[int]bool) {
	for e := err; e != nil; {
		if sc, ok := e.(statusCoder); ok {
			if status := sc.HTTPStatusCode(); status != 0 {
				statuses[status] = true
			}
			return
		}
		switch wrapped := e.(type) {
		case interface{ Unwrap() []error }:
			for _, child := range wrapped.Unwrap() {
				collectStatusCodes(child, statuses)
			}
			return
		case interface{ Unwrap() error }:
			e = wrapped.Unwrap()
		default:
			return
		}
	}
}

// BatchError joins the failures of independent items, such as the files of a
// bulk upload. Its message is Header followed by one line per failure. It
// unwraps to every failure, so each refusal's hint still prints, but no single
// item's code speaks for the batch: ParseErrorResponse and StructuredErrorCode
// do not look inside it, so a batch is never classified as one item's refusal
// (a work-session gate, an MFA step-up) and its per-item results stay in view.
type BatchError struct {
	Header   string
	Failures []error
}

func (e *BatchError) Error() string {
	lines := make([]string, len(e.Failures))
	for i, failure := range e.Failures {
		lines[i] = failure.Error()
	}
	return e.Header + ":\n  " + strings.Join(lines, "\n  ")
}

func (e *BatchError) Unwrap() []error { return e.Failures }

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

// MarkSubmission tags err, if any, as the error of the request that carries the operation's side
// effect (the command submission, an upload or download create, a session create, a one-request
// change), or of a lookup before it. Only such an error says whether the server acted, so only it
// can be retried after MFA or ridden through when unprocessed.
//
// The tag is read off the error as returned, never through wrapping, so a later request's error
// and a wrapped tagged error count as untagged, which means "the operation already took effect":
// HandleCommonErrors passes it through. The status and dial error underneath stay readable.
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

// IsUnprocessedRequestError reports whether err shows the server did not act on the request: the
// dial failed, or the server answered 429 or 503. Unlike IsTransientRequestError it leaves out a
// lost response, 502 and 504, which can follow a request that ran, so a non-idempotent request may
// be resent on it. Only a MarkSubmission-tagged error qualifies.
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
	code, _ := typedCode(err, func(code, _ string) bool { return code != "" })
	return code
}

// ParseErrorResponse prefers a typed code anywhere in the chain over text: a wrapper's
// message can quote server-supplied text, such as a rejected ID, that reads as a code.
func ParseErrorResponse(err error) (string, string) {
	if code, source := typedCode(err, func(code, source string) bool { return code != "" || source != "" }); code != "" || source != "" {
		return code, source
	}

	for e := range codeChain(err) {
		errStr := e.Error()

		start := strings.Index(errStr, "{")
		if start != -1 {
			var errorResp ErrorResponse
			if jsonErr := json.Unmarshal([]byte(errStr[start:]), &errorResp); jsonErr == nil && (errorResp.Code != "" || errorResp.Source != "") {
				return errorResp.Code, errorResp.Source
			}
		}

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

func typedCode(err error, accept func(code, source string) bool) (string, string) {
	for e := range codeChain(err) {
		if coded, ok := e.(codedError); ok {
			code, source := coded.ErrorCode(), coded.ErrorSource()
			if accept(code, source) {
				return code, source
			}
		}
	}
	return "", ""
}

// errorChain walks err and everything it wraps, joined errors included.
func errorChain(err error) iter.Seq[error] {
	return walkErrors(err, false)
}

// codeChain is errorChain without descending into a BatchError: an item's code
// is not the batch's.
func codeChain(err error) iter.Seq[error] {
	return walkErrors(err, true)
}

func walkErrors(err error, stopAtBatch bool) iter.Seq[error] {
	return func(yield func(error) bool) {
		var walk func(error) bool
		walk = func(current error) bool {
			if current == nil {
				return true
			}
			if !yield(current) {
				return false
			}
			if _, isBatch := current.(*BatchError); isBatch && stopAtBatch {
				return true
			}
			switch wrapped := current.(type) {
			case interface{ Unwrap() []error }:
				for _, child := range wrapped.Unwrap() {
					if !walk(child) {
						return false
					}
				}
			case interface{ Unwrap() error }:
				return walk(wrapped.Unwrap())
			}
			return true
		}
		walk(err)
	}
}

// LoginWithoutTokenHint is the login that opens an interactive channel; the device code
// URL is printed whether or not a browser opens, so a headless machine needs no flag.
const LoginWithoutTokenHint = "'alpacon login' without -t (on a machine without a browser, open the printed URL on another device)"

// InteractiveOnly rewrites a token's refusal of an action only an interactive login may
// take; a scope refusal counts, since granting the scope only reaches the next refusal.
// A refusal carrying the server's hint is returned as it is: the hint is the guidance.
func InteractiveOnly(err error, action string) error {
	if RefusalHint(err) != "" {
		return err
	}
	switch code, _ := ParseErrorResponse(err); code {
	case AuthInteractiveChannelRequiresPresence, AuthMFARequiresHumanPrincipal, APITokenACLNotAllowed, APITokenScopeMissing: // an older server sends APITokenACLNotAllowed for this refusal
		return fmt.Errorf("a token cannot %s; run %s and try again", action, LoginWithoutTokenHint)
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

type httpStatusError struct {
	err        error
	statusCode int
}

func (e *httpStatusError) Error() string       { return e.err.Error() }
func (e *httpStatusError) Unwrap() error       { return e.err }
func (e *httpStatusError) HTTPStatusCode() int { return e.statusCode }

// WithHTTPStatus tags err with statusCode for HTTPStatusCode.
func WithHTTPStatus(err error, statusCode int) error {
	if err == nil {
		return nil
	}
	return &httpStatusError{err: err, statusCode: statusCode}
}

// WithRedirectStatus keeps the 3xx a refused redirect leaves: Do returns it beside the error.
func WithRedirectStatus(err error, resp *http.Response) error {
	if resp == nil {
		return err
	}
	return WithHTTPStatus(err, resp.StatusCode)
}
