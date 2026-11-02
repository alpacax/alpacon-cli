package cmd

import "github.com/spf13/cobra"

var outputHelpCmd = &cobra.Command{
	Use:   "output",
	Short: "Describe the JSON output and error contract",
	Long: `Under --output json, stdout carries data and nothing else: a command's result,
a status object, or output that is the caller's own, such as a remote command's.
Help text, warnings, and progress go to stderr, so "alpacon help" and --help
print there too.

A failure ends stderr with one single-line JSON error envelope as its last line.
Anything printed before it on stderr is not JSON, so read the last line. The
process exit code equals exit_code.

Envelope fields:

  ok            false, always
  exit_code     the process exit code
  error_code    the server's code when it sent one, otherwise a CLI code
  message       plain text, no color
  command       the command path with aliases resolved, e.g. "workspace ls"
  reason        why a work session gate refused (exit 3)
  context       always present; context.operation names the command, and
                other keys carry command-specific details
  http_status   the HTTP status of the answer behind the failure
  retry_after   seconds, from the server's Retry-After
  server        the server's other error fields, as sent
  remote_exit_code  exit status of the remote command
  partial       {"count": N} when a list read failed partway; the rows read
                so far are on stdout
  next_actions  commands to run next, each with a description

Only ok, exit_code, error_code, message, and context are always present. New
fields may be added; do not reject unknown ones.

CLI error codes, used when the server sent none:

  usage_error             a flag or argument was rejected
  not_logged_in           no usable login
  confirmation_required   a prompt needed --yes on a non-interactive run
  network_error           the request got no HTTP answer
  http_<status>           an HTTP answer with no code, e.g. http_404
  not_approved            an awaited approval ended without being granted
  purpose_refused         the answer to a purpose demand was refused
  remote_command_failed   the command ran on the server and failed there
  child_command_failed    tunnel's local command exited non-zero
  cli_error               anything else

Status objects, printed on stdout with a "status" field instead of an envelope:

  exit 4  pending_approval
  exit 7  purpose_required
  exit 8  update_available, from alpacon update --check
  exit 0  up_to_date, from alpacon update --check; purpose_recorded, from
          alpacon exec purpose

Exit codes:

  0  success
  1  general error
  2  usage error
  3  work session gate denied
  4  pending approval, outcome still open
  5  server busy
  6  approval not granted
  7  purpose required
  8  a newer release exists (alpacon update --check only)`,
}

func init() {
	RootCmd.AddCommand(outputHelpCmd)
}
