package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/utils"
)

type apiError struct {
	message string
	code    string
	source  string
	// gate and missing carry the optional "gate"/"missing" fields a coded
	// 402/403/405/429 may send beside "code": the server states which gate
	// refused the request and what it was missing, but sends no human "detail"
	// on these routes, so a caller such as cmd/iam's RBAC guidance table reads
	// these instead of trying to parse one out of nothing. missing is normalized
	// to a slice regardless of whether the server sent one scope string or a list.
	gate    string
	missing []string
	// axis and next carry the optional 402 plan-limit fields: axis names which
	// limit refused the request, and next is a self-relative entitlements-read
	// path a member-scoped caller can read for the numbers the 402 body never
	// carries. planLimitMessage reads axis to build the user-facing text; next
	// is carried for a future programmatic reader, not this CLI's plain-text
	// rendering.
	axis string
	next string
	// hint is the "hint" a coded 402/403/405/429 may send beside "code": the
	// server's own sentence on what the caller can do next, already sanitized.
	// When it is set, the message identifies the refusal and leaves the
	// guidance to the hint.
	hint string
	// retryAfter is the Retry-After header's parsed delay, set by
	// withRetryAfter alongside the *retryAfterError wrapper it returns, so
	// planLimitMessage can build "resets in N days" without re-parsing the
	// header itself.
	retryAfter time.Duration
	statusCode int
	// apiPayload records that the body was a JSON object—the shape every
	// server error response has. It is a filter, not a provenance flag:
	// only the negative holds, since anything standing in front of the server
	// can emit a JSON object too. The stale-token retry reads it to drop
	// gateway refusals, and nothing may read it as proof of who wrote the body.
	apiPayload bool
	body       []byte
}

// retryAfterError carries the server's Retry-After hint so a retrying caller
// (the exec/websh poll loop) waits as long as it asked instead of guessing.
type retryAfterError struct {
	err        error
	retryAfter time.Duration
}

func (e *retryAfterError) Error() string             { return e.err.Error() }
func (e *retryAfterError) Unwrap() error             { return e.err }
func (e *retryAfterError) RetryAfter() time.Duration { return e.retryAfter }

// checkAuthStatus prefers the server-provided reason and only suggests
// re-login for a bare authentication failure—never for a coded condition such
// as MFA-required or a policy denial, which re-login does not resolve. The
// error code is always preserved on the returned error so downstream handlers
// (MFA flow, WorkSession gate) can still route on it.
func checkAuthStatus(statusCode int, body []byte) error {
	if statusCode != http.StatusUnauthorized && statusCode != http.StatusForbidden {
		return nil
	}
	detail, code, source, gate, missing, hasDetail := parseAuthStatusErrorPayload(body, statusCode)
	hint := utils.ParseRefusalHint(body, statusCode)
	return &apiError{
		message:    authStatusMessage(statusCode, code, detail, hasDetail, gate, missing, hint != ""),
		code:       code,
		source:     source,
		gate:       gate,
		missing:    missing,
		hint:       hint,
		apiPayload: isJSONObject(body),
		body:       body,
	}
}

// isJSONObject reports whether body is a JSON object. The server renders
// every error as one, so anything else on a 401—an HTML page, a bare string,
// nothing at all—came from a proxy, a WAF or an mTLS gate ahead of it. Only
// that direction holds: a JSON object clears the test whoever wrote it.
func isJSONObject(body []byte) bool {
	var parsed map[string]any
	return json.Unmarshal(body, &parsed) == nil && parsed != nil
}

// authStatusMessage renders the user-facing message for a 401/403. It prefers
// the server's human detail; absent that, a known structured code maps to a
// clear message. Re-login is suggested only for a code-less 401 or one coded
// auth_token_missing/auth_authentication_failed/auth_token_mfa_missing—a user
// who merely needs step-up MFA, or who hit a policy denial, must not be told
// to log in again.
//
// serverHint says the response carried the server's own guidance, which the
// error output prints under this message: the per-code sentence would say the
// same thing a second time, so the message then only identifies the refusal.
func authStatusMessage(statusCode int, code, detail string, hasDetail bool, gate string, missing []string, serverHint bool) string {
	if hasDetail {
		if statusCode == http.StatusUnauthorized {
			switch code {
			case "":
				return fmt.Sprintf("%s (run 'alpacon login' if your session has expired)", detail)
			case utils.AuthTokenMissing, utils.AuthAuthenticationFailed, utils.AuthTokenMFAMissing:
				return fmt.Sprintf("%s (run 'alpacon login' again)", detail)
			}
		}
		return detail
	}
	if !serverHint {
		if msg, ok := authStatusCodeMessage(statusCode, code); ok {
			return msg
		}
	}
	if statusCode == http.StatusUnauthorized {
		if code == "" {
			return reauthenticateMessage
		}
		// A coded 401 is a deliberate server decision, not a stale token; do not
		// mislabel it as an authentication failure or suggest re-login.
		// Deliberately information-light: do not interpolate the raw code into the
		// message—callers read it via ErrorCode(), and embedding it would break the
		// no-raw-code contract that TestSendRequest_403CodeWithoutDetailKeepsCodeSource guards.
		return "request denied by server"
	}
	// A coded 403 with no detail but a stated "missing": name it rather than fall
	// back to the generic line. A caller with more specific guidance for its own
	// code (cmd/iam's RBAC gates) overrides this; this is the floor for every
	// other caller that just surfaces the error as-is. "missing" is not always a
	// scope—the role gate's "missing" names a permission/role, not a
	// "scope:..." string—so only the token-scope gate gets scope wording; every
	// other gate (including an absent one, from a server this CLI does not yet
	// know) gets gate-neutral wording instead.
	if len(missing) > 0 {
		if gate == serverGateTokenScope {
			return fmt.Sprintf("permission denied: missing scope %s", strings.Join(missing, ", "))
		}
		return fmt.Sprintf("permission denied: missing %s", strings.Join(missing, ", "))
	}
	if serverHint {
		return hintedRefusalMessage(statusCode, code)
	}
	return "permission denied: you do not have the required privileges for this action"
}

// hintedRefusalMessage identifies a refusal whose guidance the server sent as
// a hint: the status and the code, with no sentence of the CLI's own.
func hintedRefusalMessage(statusCode int, code string) string {
	label := "request refused"
	if statusCode == http.StatusForbidden {
		label = "permission denied"
	}
	if code == "" {
		return fmt.Sprintf("%s (HTTP %d)", label, statusCode)
	}
	return fmt.Sprintf("%s (HTTP %d, code: %s)", label, statusCode, utils.SanitizeTerminalText(code))
}

// authStatusCodeMessage maps structured server codes that arrive on a 401/403
// without a human detail to a clear, actionable message. The login hints for
// auth_token_missing/auth_authentication_failed/auth_token_mfa_missing are
// 401-only; a 403 carrying any of them falls through to the generic
// permission-denied floor.
func authStatusCodeMessage(statusCode int, code string) (string, bool) {
	switch code {
	case utils.AuthMFARequired:
		return "multi-factor authentication required—complete MFA to continue", true
	case utils.APITokenACLNotAllowed:
		return "denied by token access control—this token may not perform that action; review its rules with 'alpacon token acl'", true
	case utils.AuthInteractiveChannelRequiresPresence:
		return "a token cannot open an interactive session—sign in with " + utils.LoginWithoutTokenHint, true
	case utils.AuthMFARequiresHumanPrincipal:
		return "this action requires a person to complete MFA, which a token cannot do—sign in with " + utils.LoginWithoutTokenHint + " and run it as yourself; if your workspace added it to its MFA required actions, an admin can remove it there instead", true
	case utils.WorkspaceSudoWithMFAStepUpUnavailable:
		return "this setting cannot be changed on this server because MFA sign-in is not available; a server administrator has to change it", true
	case utils.WorkspaceExecutionControlConsoleOnly:
		return "this change loosens execution control and can only be made from the web console", true
	case utils.ApprovalPolicyConsoleOnly:
		return "approval policies can only be created, changed or deleted from the web console", true
	case utils.SudoPolicyConsoleOnly:
		return "sudo policies are created or widened from the web console only; to run a command with sudo on a work session that an approver decides, use 'alpacon work-session update <ID> --sudo \"<command>\"'", true
	case utils.WorkSessionSudoBypassConsoleOnly, utils.WorkSessionSessionAuthorizedSudoConsoleOnly:
		return "this workspace adds session-authorized sudo to a work session from the web console only; open the session in the console and add Sudo there, or use your standing sudo access", true
	case utils.SudoVerifyCredentialCannotProveMFA:
		return "this credential cannot complete sudo MFA—sign in with 'alpacon login' to complete sudo MFA", true
	case utils.SudoPolicyBypassAgentSessionNeedsPresence, utils.SudoPolicySessionAuthorizedAgentSessionNeedsPresence:
		return "an AI agent session cannot be given session-authorized sudo (--sudo) unless a person is present, which the server reads from a sign-in that completed MFA shortly before—run 'alpacon login' again from your own terminal, completing MFA, then retry right away; or drop --sudo, and each sudo command asks for MFA", true
	case utils.SudoPolicyBypassAgentSessionNeedsApproval, utils.SudoPolicySessionAuthorizedAgentSessionNeedsApproval:
		return "session-authorized sudo cannot be bound to an AI agent session directly—request it on the work session with 'alpacon work-session update <ID> --sudo \"<command>\"', and a person approves the change", true
	case utils.AuthTokenMissing, utils.AuthAuthenticationFailed:
		if statusCode == http.StatusUnauthorized {
			return reauthenticateMessage, true
		}
	case utils.AuthTokenMFAMissing:
		if statusCode == http.StatusUnauthorized {
			return signInMFAMessage, true
		}
	}
	return "", false
}

// parseAuthStatusErrorPayload returns ok=true with a "detail" message or with
// rendered field_errors messages; code/source/gate/missing are returned
// regardless so the WorkSession gate can route to exit 3 and a coded refusal
// without either still carries what it can.
func parseAuthStatusErrorPayload(body []byte, statusCode int) (message string, code string, source string, gate string, missing []string, ok bool) {
	// axis/next ride only on the 402 plan-limit envelope (client.go's
	// planLimitMessage), never on a 401/403; this path ignores them, and
	// isEnvelopeOnly's own statusCode gate keeps a same-named validation field
	// from being swallowed regardless.
	message, code, source, gate, _, _, missing, ok = parseAPIErrorPayload(body, statusCode)
	if !ok || message == "" {
		return "", code, source, gate, missing, false
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", code, source, gate, missing, false
	}
	if stringField(parsed, "detail") != "" {
		return message, code, source, gate, missing, true
	}
	if len(fieldErrorMessages(parsed["field_errors"])) > 0 {
		return message, code, source, gate, missing, true
	}
	return "", code, source, gate, missing, false
}

func (e *apiError) Error() string {
	return e.message
}

func (e *apiError) ErrorCode() string {
	return e.code
}

func (e *apiError) ErrorSource() string {
	return e.source
}

func (e *apiError) ErrorGate() string {
	return e.gate
}

func (e *apiError) ErrorMissing() []string {
	return e.missing
}

func (e *apiError) ErrorHint() string {
	return e.hint
}

func (e *apiError) ErrorFields() map[string]any {
	return errorExtraFields(e.body)
}

func (e *apiError) HTTPStatusCode() int {
	return e.statusCode
}

func newAPIError(message, code, source, gate, axis, next string, missing []string) *apiError {
	return &apiError{message: message, code: code, source: source, gate: gate, axis: axis, next: next, missing: missing}
}

func redirectRefusedStatus(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

// withStatus tags err with its HTTP status so callers can tell 404 from 401,
// wrapping non-*apiError errors so the status survives the error chain.
func withStatus(err error, statusCode int) error {
	if err == nil {
		return nil
	}
	var ae *apiError
	if errors.As(err, &ae) {
		ae.statusCode = statusCode
		return err
	}
	return utils.WithHTTPStatus(err, statusCode)
}

// withRetryAfter tags err with the Retry-After delay. Only delta-seconds is parsed—
// that is what a throttled response sends, and misreading an HTTP-date would
// stall a poll.
// When err is (or wraps) an *apiError, the delay is also stored there directly so
// planLimitMessage can read it without re-parsing the header or unwrapping the
// *retryAfterError this still returns for every other caller (the exec/websh poll
// loops read RetryAfter() off that wrapper).
func withRetryAfter(err error, header http.Header) error {
	if err == nil {
		return nil
	}
	retryAfter := utils.RetryAfterFromHeader(header)
	if retryAfter == 0 {
		return err
	}
	var ae *apiError
	if errors.As(err, &ae) {
		ae.retryAfter = retryAfter
	}
	return &retryAfterError{err: err, retryAfter: retryAfter}
}

// parseAPIError extracts a human-readable error message from a JSON API error response.
// Handles common formats: {"detail": "..."}, {"field": ["error", ...]}, {"non_field_errors": ["..."]},
// {"field_errors": {"path": [{"code", "message"}, ...]}}
// statusCode gates whether "axis"/"next" are read as the 402 plan-limit
// envelope (isEnvelopeOnly) rather than as ordinary validation fields.
func parseAPIError(body []byte, statusCode int) error {
	message, code, source, gate, axis, next, missing, _ := parseAPIErrorPayload(body, statusCode)
	err := newAPIError(message, code, source, gate, axis, next, missing)
	err.hint = utils.ParseRefusalHint(body, statusCode)
	err.body = body
	// A message that is codeOnlyMessage's own sentence for the code gives way to
	// the server's hint, as authStatusMessage's table does.
	if err.hint != "" && code != "" && message == codeOnlyMessage(code) {
		err.message = hintedRefusalMessage(statusCode, code)
	}
	return err
}

func errorExtraFields(body []byte) map[string]any {
	var parsed map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber() // json.Number re-encodes verbatim; float64 would round past 2^53
	if decoder.Decode(&parsed) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil
	}
	if _, isEnvelopeCode := parsed["code"].(string); isEnvelopeCode {
		delete(parsed, "code")
	}
	if len(parsed) == 0 {
		return nil
	}
	return parsed
}

func parseAPIErrorPayload(body []byte, statusCode int) (message string, code string, source string, gate string, axis string, next string, missing []string, ok bool) {
	raw := string(body)

	if strings.TrimSpace(raw) == "" {
		return "server returned an empty error response", "", "", "", "", "", nil, false
	}

	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		// Not valid JSON (e.g., HTML error page) — return truncated
		return truncateBody(raw), "", "", "", "", "", nil, false
	}

	code = stringField(parsed, "code")
	source = stringField(parsed, "source")
	gate, missing = parseGateAndMissing(parsed)
	axis = stringField(parsed, "axis")
	next = stringField(parsed, "next")

	// Case 1: {"detail": "..."}
	if detail := strings.TrimSpace(stringField(parsed, "detail")); detail != "" {
		return detail, code, source, gate, axis, next, missing, true
	}

	// A refusal envelope carries no field errors; gate/axis/next/missing are not validation messages.
	if code != "" && isEnvelopeOnly(parsed, statusCode) {
		return codeOnlyMessage(code), code, source, gate, axis, next, missing, true
	}

	if messages := fieldErrorMessages(parsed["field_errors"]); len(messages) > 0 {
		return strings.Join(messages, "; "), code, source, gate, axis, next, missing, true
	}

	// Case 2: field validation errors {"field": ["msg1", ...]}. A "code" whose
	// value is the envelope's string code stays out; a list "code" field is a
	// real request field and is rendered like any other. Keys are sorted
	// for deterministic output.
	fields := make([]string, 0, len(parsed))
	for field := range parsed {
		if field == "field_errors" {
			continue
		}
		if field == "code" {
			if _, isString := parsed[field].(string); isString {
				continue
			}
		}
		// A refusal's string "hint" is the server's guidance, which reaches the
		// output sanitized through ErrorHint; rendered here it would print a
		// second time, raw. A list "hint", or one on any other status, is a
		// request field like any other.
		if field == "hint" && utils.IsRefusalStatus(statusCode) {
			if _, isString := parsed[field].(string); isString {
				continue
			}
		}
		fields = append(fields, field)
	}
	sort.Strings(fields)

	var messages []string
	for _, field := range fields {
		switch v := parsed[field].(type) {
		case []any:
			for _, item := range v {
				if s, ok := item.(string); ok {
					if field == "non_field_errors" {
						messages = append(messages, s)
					} else {
						messages = append(messages, fmt.Sprintf("%s: %s", field, s))
					}
				}
			}
		case string:
			messages = append(messages, fmt.Sprintf("%s: %s", field, v))
		}
	}

	if len(messages) > 0 {
		return strings.Join(messages, "; "), code, source, gate, axis, next, missing, true
	}

	if code != "" {
		return codeOnlyMessage(code), code, source, gate, axis, next, missing, true
	}

	// Fallback: return truncated raw body
	return truncateBody(raw), code, source, gate, axis, next, missing, true
}

// isEnvelopeOnly reports whether parsed holds only envelope-shaped values: a
// string for "code"/"source"/"gate"/"detail", "axis"/"next" the same but only
// on a 402 (the plan-limit fields—on any other status they are not part of
// the envelope, so a validation response that happens to have a field named
// "axis" or "next" still renders as a field error rather than being swallowed
// into a code-only message), "hint" the same but on any refusal status, and
// for "missing" either a single scope string or a list of them.
func isEnvelopeOnly(parsed map[string]any, statusCode int) bool {
	for key, value := range parsed {
		switch key {
		case "code", "source", "gate", "detail":
			if _, ok := value.(string); !ok {
				return false
			}
		case "hint":
			if !utils.IsRefusalStatus(statusCode) {
				return false
			}
			if _, ok := value.(string); !ok {
				return false
			}
		case "axis", "next":
			if statusCode != http.StatusPaymentRequired {
				return false
			}
			if _, ok := value.(string); !ok {
				return false
			}
		case "missing":
			if _, ok := value.(string); ok {
				continue
			}
			if !isStringList(value) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// isStringList reports whether v is a JSON array of strings, "missing"'s
// list shape in the envelope.
func isStringList(v any) bool {
	list, ok := v.([]any)
	if !ok {
		return false
	}
	for _, item := range list {
		if _, ok := item.(string); !ok {
			return false
		}
	}
	return true
}

// fieldErrorMessages renders a "field_errors" object—{"path": [{"code",
// "message"}, ...]}—into sorted "path: message" strings, "non_field_errors"
// bare. A malformed entry contributes nothing.
func fieldErrorMessages(raw any) []string {
	fieldErrors, ok := raw.(map[string]any)
	if !ok {
		return nil
	}

	paths := make([]string, 0, len(fieldErrors))
	for path := range fieldErrors {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	var messages []string
	for _, path := range paths {
		entries, ok := fieldErrors[path].([]any)
		if !ok {
			continue
		}
		for _, entry := range entries {
			fields, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			message := stringField(fields, "message")
			if message == "" {
				continue
			}
			if path == "non_field_errors" {
				messages = append(messages, message)
			} else {
				messages = append(messages, fmt.Sprintf("%s: %s", path, message))
			}
		}
	}
	return messages
}

// codeOnlyMessage renders a code sent with no detail or field errors; a case
// here names the remedy where the bare code would not.
func codeOnlyMessage(code string) string {
	switch code {
	case utils.APINotFound:
		return "the requested resource was not found"
	case utils.APIRateLimited:
		return "too many requests—please try again later"
	case utils.APINotAcceptable:
		return "the server cannot produce a response in the format requested"
	case utils.APIUnsupportedMediaType:
		return "the server does not support the request's content type"
	case utils.AuthVerificationUnavailable:
		return "the server could not verify your credential right now—it was not rejected, so try again in a moment"
	case utils.APICursorExpired:
		return "the search snapshot this page was read from expired—read the listing again from the start"
	case utils.APIInvalidCursor:
		return "the server would not take this page's cursor—read the listing again from the start"
	case utils.APISearchUnavailable:
		return "the search backend would not start this read right now—try again in a moment"
	case utils.ServerPAMInstallUnsupported:
		return "alpamon-pam cannot be installed on this server—its operating system or package manager has no alpamon-pam package"
	case utils.ServerPAMInstallRequiresSudoWithMFA:
		return "alpamon-pam needs sudo with MFA—turn on \"Use sudo with MFA\" in the workspace security settings and try again; a deployment that cannot complete the MFA step-up cannot install it from Alpacon"
	case utils.ServerPAMInstallInProgress:
		return "an alpamon-pam install is already pending on this server—wait for it to finish"
	case utils.WorkSessionTargetAccountAbsent:
		return "the account does not exist on at least one target server and will not be created there—drop that server or name a different account with --account"
	case utils.WorkSessionTargetAccountNotReachable:
		return "your roles cannot open that account on at least one target server—ask for a group role on that server, or name an account you can open with --account"
	case utils.TargetAccountOtherPerson:
		return "that account belongs to another person on at least one target server, and no session may run as it—name your own account or a shared one with --account"
	case utils.WorkSessionAgentSessionRequired:
		return "this CLI runs under an AI agent, and the work session is a user session—create an agent session with 'alpacon work-session create' and pass its ID with --work-session"
	case utils.CommandACLMoved:
		return "command ACLs are now managed on the command pre-approval ledger, which this CLI cannot change yet—a Command ACL admin issues or revokes the entry under Pre-approved commands on the service token's page in the web console; a personal API token has no command list on the ledger"
	default:
		return fmt.Sprintf("request failed (code: %s)", code)
	}
}

// parseGateAndMissing extracts the optional "gate" and "missing" fields a coded
// 402/403/405/429 response may carry beside "code": the server states which
// gate refused the request and what it was missing, but sends no human "detail"
// on these routes, so a caller such as cmd/iam's RBAC guidance table reads these
// instead. "missing" is accepted as either one scope string or a list of them—
// either way it comes back as a slice, trimmed and with empty entries dropped.
func parseGateAndMissing(parsed map[string]any) (gate string, missing []string) {
	gate = stringField(parsed, "gate")
	switch v := parsed["missing"].(type) {
	case string:
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			missing = []string{trimmed}
		}
	case []any:
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				continue
			}
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				missing = append(missing, trimmed)
			}
		}
	}
	return gate, missing
}

func stringField(values map[string]any, field string) string {
	value, _ := values[field].(string)
	return strings.TrimSpace(value)
}

func truncateBody(s string) string {
	const maxLen = 200
	if len(s) > maxLen {
		return s[:maxLen] + "... (truncated)"
	}
	return s
}
