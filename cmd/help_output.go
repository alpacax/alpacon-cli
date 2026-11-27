package cmd

import "github.com/spf13/cobra"

func newOutputHelpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "output",
		Short: "Understand the output contract",
		Long: `Stdout carries data only: tables, or JSON results under --output json.
Messages, warnings, progress, and help under --output json go to stderr.

These pass another program's output through to stdout, so --output json
cannot make it JSON:
  exec, websh with a command, exec logs   the remote command's output
  interactive websh, websh join/watch     the terminal session
  tunnel                                  its local command's output
  edit and others that open $EDITOR       the editor
  api                                     the response body
  work-session recording                  the recording
  user create                             the generated password
  completion                              the script

Under --output json a few outcomes print one object on stdout instead of a
result or an error object:
  exit 0  {"ok":true,"status":"running"}  exec logs, command not finished
  exit 4  {"status":"pending_approval"}   waiting on a human approver
  exit 7  {"status":"purpose_required"}   the gate asks what it is for
  exit 8  {"update_available":true}       update --check found a release

Every other failure under --output json ends stderr with one single-line
JSON object. Its fields are ok (false), exit_code (the process exit code,
never 0), error_code, message, context.operation, and usually command.
Optional fields, only ever added: http_status, retry_after, reason
(why a work session refused it), server, next_actions, and partial.count
(rows already on stdout).

error_code is the server's code when it sent one, otherwise the CLI's own:
usage_error, confirmation_required, not_logged_in, network_error,
not_approved, mfa_required, cli_error, remote_command_failed,
local_command_failed, purpose_refused, or http_<status>.

Exit codes:
  0  success, or nothing has failed yet
  1  general error
  2  usage error: fix the command line rather than retry it unchanged
  3  refused by the work session gate
  4  waiting on a human approver: check again later
  5  the server is busy with user work
  6  not approved: stop requesting approval again
  7  purpose required: answer what the command is for
  8  a newer release exists
  9  MFA required: finish MFA in a browser, then run the command again`,
	}
}
