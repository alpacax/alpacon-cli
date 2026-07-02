package utils

import (
	"encoding/json"
	"errors"
	"strings"
)

const (
	AuthMFARequired  = "auth_mfa_required"
	UsernameRequired = "user_username_required"

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
)

// WorkSessionGateCodes is the single source of truth enumerating the WorkSession
// gate codes above, for callers (e.g. skill drift tests) that need the full set.
var WorkSessionGateCodes = []string{
	WorkSessionRequired,
	WorkSessionNotUsable,
	WorkSessionNotActive,
	WorkSessionExpired,
	WorkSessionScopeNotAllowed,
	WorkSessionServerNotAllowed,
	WorkSessionAssigneeMismatch,
}

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
		for _, part := range strings.Split(errStr, "; ") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "code: ") {
				iterCode = strings.TrimPrefix(part, "code: ")
			} else if strings.HasPrefix(part, "source: ") {
				iterSource = strings.TrimPrefix(part, "source: ")
			}
		}
		if iterCode != "" {
			return iterCode, iterSource
		}
	}

	return "", ""
}
