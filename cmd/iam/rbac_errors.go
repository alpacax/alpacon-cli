package iam

import (
	"errors"
	"net/http"

	"github.com/alpacax/alpacon-cli/api/rbac"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

// Error codes returned by the RBAC binding endpoints (alpacon-server utils/error_codes.py).
const (
	codeAdminLastRemoval     = "rbac_admin_last_removal_forbidden"
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
	// gatePermissionIntrospect: /api/iam/users/{id}/permissions/ pins no scope, so by UUID it
	// auto-resolves to an orphan 'user:permissions' that only the superuser wildcard grants;
	// the "-" self alias skips the check entirely.
	gatePermissionIntrospect
)

type rbacGate int

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
		return errors.New("this is the workspace's last admin, and a workspace cannot be left without one; grant 'admin' to someone else first")
	case codeSuperuserLastRemoval:
		return errors.New("this is the workspace's last superuser, and a workspace cannot be left without one; grant 'superuser' to someone else first")
	// Unreachable from 'grant', which absorbs duplicates as convergence; kept for any later
	// caller that writes a binding without doing the same.
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
			return errors.New("reading another account's effective permissions requires the user:read permission on that account; your own are always readable")
		case gate == gatePermissionIntrospect:
			return errors.New("this endpoint pins no permission of its own, so a cross-account read of it is satisfied only by a wildcard grant—in practice the superuser role. Your own permissions are always readable; run the command without a USER argument")
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
