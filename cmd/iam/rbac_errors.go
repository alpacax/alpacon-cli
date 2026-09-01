package iam

import (
	"net/http"

	"github.com/alpacax/alpacon-cli/api/rbac"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

// Error codes this surface can receive (alpacon-server utils/error_codes.py). All but
// permission_denied come from the binding endpoints; that one is the troubleshoot read.
const (
	codeAdminLastRemoval     = "rbac_admin_last_removal_forbidden"
	codePermissionDenied     = "permission_denied"
	codeSuperuserLastRemoval = "rbac_superuser_last_removal_forbidden"
	codeBulkLimitExceeded    = "rbac_bulk_limit_exceeded"
	codeInvalidInput         = "invalid_input"
	codeWorkspaceSuspended   = "workspace_suspended"
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
	// gateAuditRead: the audit log is the one /api/rbac/ route exempt from the Auth0 gate, so
	// an API token reaches it and is refused only for missing role_audit_log:read.
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
// ParseErrorResponse walk it—while Error() prints only the actionable message: the
// refusal it replaces is a bare code or DRF's generic sentence.
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
	case codeInvalidInput:
		return rewrite(err, "the server rejected the binding scope")
	case codeWorkspaceSuspended:
		return rewrite(err, "this workspace is suspended, so it accepts no changes")
	case codePermissionDenied:
		return rewrite(err, "that is not an account you may read; your own is always readable")
	}

	if utils.HTTPStatusCode(err) == http.StatusForbidden && code == "" {
		switch {
		case gate == gateUserRead:
			return rewrite(err, "reading another account's effective permissions requires the user:read permission on that account; your own are always readable")
		case gate == gatePermissionIntrospect:
			return rewrite(err, "this endpoint pins no permission of its own, so a cross-account read of it is satisfied only by a wildcard grant—in practice the superuser role. Your own permissions are always readable; run the command without a USER argument")
		case gate == gateAuditRead && !ac.IsBearerAuth():
			return rewrite(err, "the role history is limited to an auditor of the whole workspace; everyone else sees only the changes naming them. An API token additionally needs the role_audit_log:read scope")
		case gate == gateAuditRead:
			return rewrite(err, "reading another account's role history requires an auditor of the whole workspace; everyone else sees only the changes naming them")
		case gate == gateRoleWrite && !ac.IsBearerAuth():
			return rewrite(err, "a role-binding write requires the superuser role, and this credential may be refused outright: the RBAC API accepts no API token on an Alpacon Cloud workspace. Run 'alpacon login' to authenticate through the browser")
		case gate == gateRoleWrite:
			return rewrite(err, "a role-binding write requires the superuser role")
		case !ac.IsBearerAuth():
			// Lead with the reading that holds on both deployments: the credential refusal exists
			// only where the Auth0 gate is installed, and self-hosted does not install it.
			return rewrite(err, "your account may not see the account or role named. On an Alpacon Cloud workspace the cause is the credential instead: the RBAC API refuses API tokens there, so run 'alpacon login' to authenticate through the browser")
		default:
			return rewrite(err, "this workspace refused the read without stating a reason, which usually means your account may not see the account or role named")
		}
	}

	return err
}

func (e *rewritten) Error() string { return e.message }

func (e *rewritten) Unwrap() error { return e.cause }

func rewrite(cause error, message string) error {
	return &rewritten{message: message, cause: cause}
}
