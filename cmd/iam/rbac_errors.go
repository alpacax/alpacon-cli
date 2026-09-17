package iam

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/alpacax/alpacon-cli/api/rbac"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

// Codes this surface receives. All but permission_denied come from the binding endpoints;
// that one is the troubleshoot read. The four below are new: alpacon-server used to answer
// every role and token-scope refusal with a bare DRF {"detail": ...} 403, and now answers
// with one of these instead, carrying no detail of its own—the message has to be the CLI's.
const (
	codeAdminLastRemoval     = "rbac_admin_last_removal_forbidden"
	codePermissionDenied     = "permission_denied"
	codeSuperuserLastRemoval = "rbac_superuser_last_removal_forbidden"
	codeBulkLimitExceeded    = "rbac_bulk_limit_exceeded"
	codeInvalidInput         = "invalid_input"
	codeWorkspaceSuspended   = "workspace_suspended"

	// codeRolePermissionRequired and codeRoleObjectPermissionRequired are the role
	// gate's refusals—missing the permission outright, or missing it on the specific
	// object named. Both route through the same per-gate guidance a code-less 403
	// from the role gate always got: the refusal reason has not changed, only
	// whether the server states a code for it.
	codeRolePermissionRequired       = "rbac_permission_required"
	codeRoleObjectPermissionRequired = "rbac_object_permission_required"

	// codeTokenScopeMissing and codeTokenScopeActionUnresolved are the token-scope
	// gate's refusals—an API token whose bound scopes do not cover this call, or a
	// call whose action the server could not resolve to a scope at all.
	codeTokenScopeMissing          = "api_token_scope_missing"
	codeTokenScopeActionUnresolved = "api_token_scope_action_unresolved"
)

// Gates for describeRBACError. gateRoleRead is first so the zero value is the safest gate.
const (
	// gateRoleRead is a read on /api/rbac/. An API token is refused outright on an
	// Alpacon Cloud workspace. The role and binding reads narrow a caller who cannot see
	// the target to 200 rather than refusing, so an uncoded 403 from them is unexpected;
	// the troubleshoot read is the exception, and it refuses with a code.
	gateRoleRead rbacGate = iota
	// gateRoleWrite is a binding write: the superuser role, plus the same token refusal.
	gateRoleWrite
	// gateAuditRead: the audit log is the one /api/rbac/ route that accepts an API token on
	// every deployment, so its only refusal is a missing role_audit_log:read scope.
	gateAuditRead
	// gateUserRead: /api/iam/users/{id}/effective-permissions/ pins user:read, and API tokens
	// are accepted on the IAM routes, so a refusal is about the permission, not the credential.
	gateUserRead
	// gatePermissionIntrospect: /api/iam/users/{id}/permissions/ pins no scope, so it
	// auto-resolves to an orphan 'user:permissions': cross-account only a wildcard grant
	// satisfies it, while a self read passes through user:owner's 'user:*'.
	gatePermissionIntrospect
)

type rbacGate int

// rewritten keeps the server's error in the chain—utils.HTTPStatusCode and
// ParseErrorResponse walk it—while Error() prints only the actionable message.
type rewritten struct {
	message string
	cause   error
}

// describeRBACError rewrites RBAC refusals into something an operator can act on: the
// coded ones carry no human detail, and the likeliest refusal is a 403 with no code,
// which needs both the gate and the credential to name a fix that can work.
//
// The credential kind must come off the client: config.IsSaaS only reports that an
// access token is stored, which cannot separate a refused Alpacon Cloud token session
// from a working self-hosted one.
func describeRBACError(ac *client.AlpaconClient, gate rbacGate, err error) error {
	if err == nil {
		return nil
	}

	code, _ := utils.ParseErrorResponse(err)
	switch code {
	case codeAdminLastRemoval:
		return rewrite(err, "this is the workspace's last admin, and a workspace cannot be left without one; grant 'admin' to someone else first")
	case codeSuperuserLastRemoval:
		return rewrite(err, "this is the workspace's last superuser, and a workspace cannot be left without one; grant 'superuser' to someone else first")
	// Unreachable from 'grant', which absorbs duplicates as convergence; kept for any later
	// caller that writes a binding without doing the same.
	case rbac.CodeRoleAssignmentDuplicate:
		return rewrite(err, "that role is already bound to the user at this scope")
	case codeBulkLimitExceeded:
		return rewrite(err, "the server refused the request as a bulk operation; this is a bug in the CLI, which binds one role to one user per request")
	// invalid_input reaches every call site, so the message cannot name the binding scope.
	case codeInvalidInput:
		return rewrite(err, "the server rejected one of the request's values")
	case codeWorkspaceSuspended:
		return rewrite(err, "this workspace is suspended, so it accepts no changes")
	case codePermissionDenied:
		return rewrite(err, permissionDeniedMessage(gate))
	case codeTokenScopeMissing:
		return rewrite(err, tokenScopeMissingMessage(err))
	case codeTokenScopeActionUnresolved:
		return rewrite(err, "this token cannot be used for this command; use a login session or a token with an explicit scope")
	}

	// codeRolePermissionRequired and codeRoleObjectPermissionRequired are the coded
	// shape of the same role-gate refusal the code-less 403 below has always meant—
	// widen the guard rather than duplicate the five gate-specific messages.
	if utils.HTTPStatusCode(err) == http.StatusForbidden && isRoleGateCode(code) {
		switch {
		case gate == gateUserRead:
			return rewrite(err, "reading another account's effective permissions requires the user:read permission on that account; your own are always readable")
		case gate == gatePermissionIntrospect:
			return rewrite(err, "this endpoint pins no permission of its own, so a cross-account read of it is satisfied only by a wildcard grant—in practice the superuser role. Your own permissions are always readable; run the command without a USER argument")
		// A narrowed audit view comes back as a short list, not a refusal, so a missing scope
		// is the only 403 left here; a bearer, which that gate cannot refuse, hits the default.
		case gate == gateAuditRead && !ac.IsBearerAuth():
			return rewrite(err, "this API token is missing the role_audit_log:read scope, which the role history requires. Widen the token's scopes, or run 'alpacon login' to read it through a browser session")
		case gate == gateRoleWrite && !ac.IsBearerAuth():
			return rewrite(err, "a role-binding write requires the superuser role, and this credential may be refused outright: the RBAC API accepts no API token on an Alpacon Cloud workspace. Run 'alpacon login' to authenticate through the browser")
		case gate == gateRoleWrite:
			return rewrite(err, "a role-binding write requires the superuser role")
		case !ac.IsBearerAuth():
			// Lead with the reading that holds on both deployments: only Alpacon Cloud refuses the token.
			return rewrite(err, "your account may not see the account or role named. On an Alpacon Cloud workspace the cause is the credential instead: the RBAC API refuses API tokens there, so run 'alpacon login' to authenticate through the browser")
		default:
			return rewrite(err, "this workspace refused the read without stating a reason, which usually means your account may not see the account or role named")
		}
	}

	return err
}

// isRoleGateCode reports whether code is compatible with the per-gate 403 guidance
// above: either no code at all (the legacy DRF {"detail": ...} refusal every
// call site here predates) or one of the role gate's two coded refusals, whose
// payload states no human detail and means exactly what the code-less 403
// always meant.
func isRoleGateCode(code string) bool {
	switch code {
	case "", codeRolePermissionRequired, codeRoleObjectPermissionRequired:
		return true
	default:
		return false
	}
}

// permissionDeniedMessage renders codePermissionDenied, the troubleshoot read's
// refusal. Every other call site of that code is a read (user_permission_list,
// user_permission_cani), where "that is not an account you may read" holds—but
// describeRBACError is reachable from the role-write gate too, and that wording
// would misdescribe a write refusal as a visibility problem.
func permissionDeniedMessage(gate rbacGate) string {
	if gate == gateRoleWrite {
		return "you do not have permission to make that change"
	}
	return "that is not an account you may read; your own is always readable"
}

// tokenScopeMissingMessage names the scope(s) the token lacks, read off the
// payload's "missing" field (a single scope string or a list of them). The
// server sends no detail on this code, so without this the operator would see
// only the generic client-side fallback and not which scope to add.
func tokenScopeMissingMessage(err error) string {
	_, missing := utils.ParseErrorGateAndMissing(err)
	if len(missing) == 0 {
		return "this API token is missing a scope this command requires; widen the token's scopes, or run 'alpacon login' to authenticate through a browser session"
	}
	return fmt.Sprintf("this API token is missing the required scope(s): %s; widen the token's scopes, or run 'alpacon login' to authenticate through a browser session", strings.Join(missing, ", "))
}

func (e *rewritten) Error() string { return e.message }

func (e *rewritten) Unwrap() error { return e.cause }

func rewrite(cause error, message string) error {
	return &rewritten{message: message, cause: cause}
}
