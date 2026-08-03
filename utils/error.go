package utils

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

const (
	AuthMFARequired  = "auth_mfa_required"
	UsernameRequired = "user_username_required"

	// ServerBusyWithUserWork: disruptive action refused; --force overrides it.
	ServerBusyWithUserWork = "server_busy_with_user_work"

	// WorkSession gate codes (returned by alpacon-server work_sessions/services.py)
	WorkSessionRequired         = "work_session_required"
	WorkSessionNotUsable        = "work_session_not_usable"
	WorkSessionNotActive        = "work_session_not_active"
	WorkSessionExpired          = "work_session_expired"
	WorkSessionScopeNotAllowed  = "work_session_scope_not_allowed"
	WorkSessionServerNotAllowed = "work_session_server_not_allowed"
	WorkSessionAssigneeMismatch = "work_session_assignee_mismatch"

	// ExitCodeWorkSessionDenied is the process exit code for WorkSession gate refusals.
	ExitCodeWorkSessionDenied = 3

	// ExitCodePendingApproval is the process exit code for an action that landed
	// pending human approval (a sudo HITL SUDO_APPROVAL_REQUIRED denial, or a work
	// session created in the pending state) and was not waited on with --wait.
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

// HTTPStatusCode returns the HTTP status carried by err, or 0 if none—lets callers tell 404 from 401.
func HTTPStatusCode(err error) int {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if sc, ok := e.(statusCoder); ok {
			return sc.HTTPStatusCode()
		}
	}
	return 0
}

// IsFatalClientError reports whether a 4xx will fail the same way on retry—408 and 429
// ask for exactly that retry, so they are not fatal.
func IsFatalClientError(status int) bool {
	if status == http.StatusRequestTimeout || status == http.StatusTooManyRequests {
		return false
	}
	return status >= http.StatusBadRequest && status < http.StatusInternalServerError
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
