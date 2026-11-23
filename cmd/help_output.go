package cmd

import "github.com/spf13/cobra"

func newOutputHelpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "output",
		Short: "Understand the output contract",
		Long: `Stdout carries data only: tables, or JSON results under --output json.
Messages, warnings, progress, and help under --output json go to stderr.

These write raw text to stdout, which --output json does not make JSON: exec,
websh with a command, and exec logs (the remote command's output), interactive
websh, websh join, and websh watch (the terminal session), tunnel (its local
command's output), edit and other commands that open $EDITOR (the editor), api
(the response body), work-session recording, user create (a generated
password), and completion (the script).

Under --output json every failure ends stderr with one single-line JSON
object, except an outcome that is still open (exit 4 or 7) or an available
update (exit 8), which is an object on stdout. Two failures print no object: a
permission that 'user permission can-i -q' finds denied, and a password prompt
ended by Ctrl+C. A can-i -q check that itself fails prints one.

The fields are ok (false), exit_code (the process exit code: on Unix the low 8
bits of a remote status, and never 0), error_code, message, context.operation,
and usually command. Optional fields, only ever added: http_status,
retry_after, server, next_actions, remote_exit_code (the remote command's own
status), and partial.count (rows already on stdout).

error_code is the server's code when it sent one, otherwise the CLI's own:
usage_error, confirmation_required, not_logged_in, network_error,
not_approved, mfa_required, cli_error, remote_command_failed,
local_command_failed, purpose_refused, or http_<status>. Exit codes are listed in the README.`,
	}
}
