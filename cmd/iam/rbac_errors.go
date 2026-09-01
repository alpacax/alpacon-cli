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

// describeRBACError rewrites what the RBAC endpoints answer into something an
// operator can act on.
//
// Two things make the raw error unhelpful. The coded refusals carry no human
// detail, so they surface as a bare snake_case token. And the refusal an operator
// is most likely to hit—any RBAC request made with an API token against an Alpacon
// Cloud workspace, reads included—arrives as a 403 with no code at all, which is
// indistinguishable from simply lacking the privilege.
//
// The credential kind is the tiebreaker for that codeless 403. It has to come off
// the client: config.IsSaaS reports only whether an access token is stored, which
// cannot separate an Alpacon Cloud token session, which is refused, from a
// self-hosted one, which works.
func describeRBACError(ac *client.AlpaconClient, err error) error {
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
		// Lead with the cause that is possible on every deployment and every route. The
		// credential-kind refusal is real but narrower: it applies to the /api/rbac/
		// routes on an Alpacon Cloud workspace, and the audit log answers the same bare
		// 403 for a token that simply lacks a scope. Naming one of those as the cause
		// sends an operator to re-run 'alpacon login' for a refusal a login cannot move.
		if !ac.IsBearerAuth() {
			return errors.New("refused without a stated reason. A role-binding write requires the superuser role. This credential may also be the problem: the RBAC API refuses API tokens outright on Alpacon Cloud workspaces—run 'alpacon login' to authenticate through the browser—and the role history needs a token carrying the role_audit_log:read scope")
		}
		return errors.New("refused without a stated reason. A role-binding write requires the superuser role; a read is limited to the accounts and roles this workspace lets you see")
	}

	return err
}
