package cmd

import "github.com/spf13/cobra"

func newOutputHelpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "output",
		Short: "Understand the output contract",
		Long: `Stdout carries data only: tables, or JSON results under --output json.
Messages, warnings, progress, and help under --output json go to stderr.

Under --output json every failure ends stderr with one single-line JSON
object: ok (false), exit_code (the process exit code), error_code, message,
context.operation, and usually command. Optional fields, only ever added:
http_status, retry_after, server, next_actions, remote_exit_code (the remote
command's own status), and partial.count (rows already on stdout).

error_code is the server's code when it sent one, otherwise the CLI's own:
usage_error, confirmation_required, not_logged_in, network_error,
not_approved, cli_error, remote_command_failed, local_command_failed,
purpose_refused, or http_<status>. Exit codes are listed in the README.`,
	}
}
