package iam

import (
	"errors"
	"net/http"

	"github.com/alpacax/alpacon-cli/api/rbac"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

// Error codes returned by the RBAC binding endpoints (alpacon-server
// utils/error_codes.py). The server's envelope on these is a bare {"code": "..."}
// with no human detail, so every one of them has to be rewritten here or the
// operator sees the raw code.
const (
	codeAdminLastRemoval     = "rbac_admin_last_removal_forbidden"
	codeSuperuserLastRemoval = "rbac_superuser_last_removal_forbidden"
	codeBulkLimitExceeded    = "rbac_bulk_limit_exceeded"
	codeInvalidInput         = "invalid_input"
	codeWorkspaceSuspended   = "workspace_suspended"
)

// Gate identifiers for describeRBACError. Kept in the const block above the type so
// the zero value is the read gate, which is the safe default for a caller that
// forgets to say.
const (
	// gateRoleRead is a read on /api/rbac/. An API token is refused outright on an
	// Alpacon Cloud workspace. A caller who simply cannot see the target is narrowed to
	// 200 rather than refused, so a 403 here is never about visibility.
	gateRoleRead rbacGate = iota
	// gateRoleWrite is a binding write: the superuser role, plus the same token refusal.
	gateRoleWrite
	// gateAuditRead is the role audit log, the one /api/rbac/ route exempt from the
	// Auth0 gate. An API token reaches it on every deployment and is refused only for
	// missing the role_audit_log:read scope - the opposite failure from gateRoleRead,
	// which is why it cannot share that message.
	gateAuditRead
	// gateUserRead is an introspection read hosted on /api/iam/users/. API tokens are
	// accepted there; reading another account needs user:read on it.
	gateUserRead
)

// rbacGate names which gate a refused request went through.
type rbacGate int

// describeRBACError rewrites what the RBAC endpoints answer into something an
// operator can act on.
//
// Two things make the raw error unhelpful. The coded refusals carry no human
// detail, so they surface as a bare snake_case token. And the refusal an operator
// is most likely to hit arrives as a 403 with no code at all.
//
// That codeless 403 needs both the gate the request went through and the credential
// it carried, because the three surfaces fail for three different reasons and
// naming the wrong one sends the operator after a fix that cannot work. The
// credential kind has to come off the client: config.IsSaaS reports only whether an
// access token is stored, which cannot separate an Alpacon Cloud token session,
// which is refused, from a self-hosted one, which works.
func describeRBACError(ac *client.AlpaconClient, gate rbacGate, err error) error {
	if err == nil {
		return nil
	}

	code, _ := utils.ParseErrorResponse(err)
	switch code {
	case codeAdminLastRemoval:
		return errors.New("this is the workspace's last admin, and a workspace cannot be left without one; grant 'admin' to someone else first")
	case codeSuperuserLastRemoval:
		return errors.New("this is the workspace's last superuser, and a workspace cannot be left without one; grant 'superuser' to someone else first")
	// 'grant' absorbs this one as convergence and never reaches here; the branch
	// covers any later caller that writes a binding without doing the same.
	case rbac.CodeRoleAssignmentDuplicate:
		return errors.New("that role is already bound to the user at this scope")
	case codeBulkLimitExceeded:
		return errors.New("the server refused the request as a bulk operation; this is a bug in the CLI, which binds one role to one user per request")
	case codeInvalidInput:
		return errors.New("the server rejected the binding scope")
	case codeWorkspaceSuspended:
		return errors.New("this workspace is suspended, so it accepts no changes")
	}

	if utils.HTTPStatusCode(err) == http.StatusForbidden && code == "" {
		switch {
		case gate == gateUserRead:
			return errors.New("reading another account's permissions requires the user:read permission on that account; your own are always readable")
		case gate == gateAuditRead && !ac.IsBearerAuth():
			return errors.New("this API token is missing the role_audit_log:read scope, which the role history requires. Widen the token's scopes, or run 'alpacon login' to read it through a browser session instead")
		case gate == gateAuditRead:
			return errors.New("reading another account's role history requires an auditor of the whole workspace; everyone else sees only the changes naming them")
		case gate == gateRoleWrite && !ac.IsBearerAuth():
			return errors.New("a role-binding write requires the superuser role, and this credential may be refused outright: the RBAC API accepts no API token on an Alpacon Cloud workspace. Run 'alpacon login' to authenticate through the browser")
		case gate == gateRoleWrite:
			return errors.New("a role-binding write requires the superuser role")
		case !ac.IsBearerAuth():
			return errors.New("the RBAC API refuses API tokens on Alpacon Cloud workspaces, reads included. Run 'alpacon login' to authenticate through the browser")
		default:
			return errors.New("this workspace refused the read without stating a reason, which usually means your account may not see the account or role named")
		}
	}

	return err
}
