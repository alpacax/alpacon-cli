package cmd

import "github.com/spf13/cobra"

var outputHelpCmd = &cobra.Command{
	Use:   "output",
	Short: "Describe the JSON output and error contract",
	Long: `Under --output json, stdout carries one JSON value, or nothing, except output
that is the caller's data: a remote command's output (exec, websh, and the child
command tunnel runs after --), the response body alpacon api prints (and the
headers --include adds), a shell completion script, and the stream of one JSON
value per line that event watch prints.

A failure ends stderr with one single-line JSON error envelope as its last line.
Anything printed before it on stderr (warnings, help text) is not JSON, so read
the last line. The process exit code equals exit_code.

Envelope fields:

  ok                false, always
  exit_code         the process exit code
  error_code        the server's code when it sent one, otherwise a CLI code
  message           plain text, no color
  command           the command path, e.g. "workspace ls", when known
  context           command-specific details
  http_status       the HTTP status of the answer behind the failure
  retry_after       seconds, from the server's Retry-After
  server            the server's other error fields, as sent
  remote_exit_code  exit status of the remote command (exec, websh) or of the
                    child command (tunnel)
  partial           {"count": N} when a list read failed partway; the rows read
                    so far are on stdout

Fields other than ok, exit_code, error_code, and message appear only when they
apply. New fields may be added; do not reject unknown ones.

CLI error codes, used when the server sent none:

  usage_error             a flag or argument was rejected
  not_logged_in           no usable login
  confirmation_required   a prompt needed --yes on a non-interactive run
  network_error           the request got no HTTP answer
  http_<status>           an HTTP answer with no code, e.g. http_404
  remote_command_failed   the remote command exited non-zero
  child_command_failed    the command run by tunnel exited non-zero
  not_approved            an awaited approval ended without being granted
  purpose_refused         the purpose answer was refused
  permission_not_held     can-i -q: the permission is not held
  cli_error               anything else

Status objects, printed on stdout instead of an envelope:

  exit 4  {"status":"pending_approval", ...}
  exit 7  {"status":"purpose_required", ...}
  exit 8  {"status":"update_available", ...}, from alpacon update --check
  exit 0  {"status":"up_to_date", ...}, from alpacon update --check

Every usage error carries error_code usage_error: what Cobra rejects while
parsing and the argument checks of exec, websh, and cp exit 1, while the
work-session commands, event wait and watch, and alpacon api's own input
validation exit 2. Help text goes to stderr.`,
}
