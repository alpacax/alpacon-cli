package exec

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

// sudoDenialLinePrefix is the terminal-facing denial line the sudo plugin
// prints ("Alpacon denied this sudo command (CODE)."), up to the code slot.
// The other "Permission denied (CODE)" form only reaches the audit log—not the
// invoking terminal—so it must not be matched. Anchoring on this full prefix
// (not a bare "(CODE)") stops a command whose own output prints
// "(SUDO_RISK_DENIED)" from forging a hint.
const sudoDenialLinePrefix = "Alpacon denied this sudo command ("

// sudoDenialCodelessLine is the denial line the agent emits with no code to
// name because the plugin has none to report. Its own literal rather than
// sudoDenialLinePrefix with an empty slot—the sanitizer rejects a bad code
// whole, so the parentheses go with it and no empty-slot form ever reaches
// the terminal.
const sudoDenialCodelessLine = "Alpacon denied this sudo command."

// sudoPresenceRequiredCode is the one denial code the CLI resolves in-flow (an
// MFA step-up). The hint table and hasSudoPresenceDenial both name it from
// here, so renaming it cannot leave one of them behind and silently stop the
// step-up.
const sudoPresenceRequiredCode = "SUDO_PRESENCE_REQUIRED"

// commandInlineCredentialMessage is the exec-facing error line for the
// server's inline-credential gate (utils.CommandInlineCredential).
const commandInlineCredentialMessage = "server rejected this command—the command line carries a credential"

const (
	// usernameGroupnameInvalidCode is the code the server answers with when the
	// command carries no execution user and the server has no default one.
	usernameGroupnameInvalidCode = "server_username_groupname_invalid"
	execUserRequiredHint         = "Specify an execution user with -u USERNAME (and optionally -g GROUP), or use USERNAME@SERVER.\n%s"
)

// ExecInvocation and WebshInvocation are the two Invocation values (see the
// Invocation type below).
const (
	ExecInvocation  Invocation = "alpacon exec"
	WebshInvocation Invocation = "alpacon websh"
)

// approvalWaitMessage labels the --wait spinner. Named because each stretch of
// waiting gets a spinner of its own (see RunExecWithApprovalWait), and two
// labels drifting apart would read as two different waits.
const approvalWaitMessage = "Waiting for approval in the Alpacon console..."

const (
	// outcomePending covers every status that is not a decision, the absent
	// field included: a value nobody can read as an answer must not end a wait.
	outcomePending approvalOutcome = iota
	outcomeApproved
	// outcomeRejected and outcomeExpired both settle without a grant and both exit
	// ExitCodeNotApproved. They stay apart because the sentence the user reads
	// differs: one sends them back to the reviewer, the other to a fresh request.
	outcomeRejected
	outcomeExpired
)

// approvalWaitPollInterval is the base gap of the --wait poll loop—slower than
// the MFA poll (api/mfa/mfa.go) since each tick reads the command detail rather
// than re-running the command; a var so tests can shorten it.
var approvalWaitPollInterval = 5 * time.Second

// Test seams so a unit test can drive the deadline/resume logic without real network I/O.
var (
	runPresenceStepUp     = RunExecWithPresenceStepUp
	streamApprovedCommand = event.StreamApprovedCommand
	mfaLinkByServerName   = mfa.GetMFALinkByServerName
	// getCommandByID is the approval wait's own read of the command detail, kept
	// as a var so a test can drive it without a server.
	getCommandByID = event.GetCommandByID
)

// sudoDenialHints maps a non-interactive sudo denial code to actionable
// guidance. Codes mirror the server's by hand—nothing enforces the sync,
// which is why sudoDenialHint falls back to naming a code this table does not
// carry.
//
// The table is the fallback for a server that sends no hint of its own. A
// server that does has the sudo plugin print it on the line under the denial
// line, where the user already reads it in the command's output, so a denial
// line followed by one (serverHintFollows) gets no sentence from here.
//
// The codes are UPPERCASE because the plugin only passes [A-Z0-9_]
// codes through its sanitizer into the user-facing denial line (lowercase
// values are dropped). Each hint stays at the denial *category* level (what to
// do)—the server never sends the risk score or reasoning to a client.
//
// pendingApproval marks the codes the server emits after creating an approval
// grant: the sudo call still fails now (an interactive sudo cannot wait on an
// out-of-band approval), but a reviewer can still approve it.
// Flagging them here rather than in a second list is what keeps the hints and
// that code set from drifting apart.
//
// selfService narrows the pendingApproval codes to those whose guidance names a
// way out that is not "wait for a reviewer". HandlePendingApproval prints only
// those: its own message already covers re-running after approval, so printing
// an entry without one would say the same thing twice. pendingSudoDenial
// prefers a selfService entry over the rest, so the table order does not decide
// which pending code answers.
//
// The gates the server checks before it judges the command at all lead the
// table, in that order, so output carrying denial lines from several sudo calls
// answers with the gate the server checks earliest—not with whichever call ran
// first. The codes after them are alternative verdicts on one call, so their
// order among themselves carries no meaning.
var sudoDenialHints = []struct {
	code, guidance  string
	pendingApproval bool
	selfService     bool
}{
	{
		// Checked before the websh/command branch split, so it answers every
		// surface. Turning sudo with MFA on loosens execution control, which the
		// server accepts from the web console only, and on a self-hosted server
		// without MFA sign-in it refuses that change, so the guidance names both
		// routes.
		code: "WORKSPACE_SUDO_WITH_MFA_DISABLED",
		guidance: "sudo was denied: this workspace does not allow sudo with MFA at all, so no work session or policy can authorize it.\n" +
			"A workspace administrator can turn sudo with MFA on from the web console (Settings \u2192 Access control); the CLI cannot make this change.\n" +
			"On a self-hosted server without MFA sign-in this setting cannot be changed from the web console either; a server administrator has to change it.\n",
	},
	{
		// The server cannot run sudo MFA at all (typically self-hosted without
		// MFA sign-in), so a step-up would never complete.
		code: "SUDO_STEP_UP_UNAVAILABLE",
		guidance: "sudo was denied: sudo with MFA is not available on this server.\n" +
			"Use a work session whose policy allows the command without MFA, or ask an administrator.\n",
	},
	{
		// Nothing about the command line is wrong, so a re-run on a fresh
		// command is the only move.
		code:     "SUDO_SESSION_MISSING",
		guidance: "sudo was denied: the server could not match this sudo call to its command. Re-run the command; if it repeats, the agent and the server disagree about the command's state.\n",
	},
	{
		// Wording follows validateSessionForSudoUpdate in
		// cmd/worksession/worksession_update.go, which answers the same gap.
		code: "WORK_SESSION_SCOPE_NOT_ALLOWED",
		guidance: "sudo was denied: your work session does not include the 'sudo' scope, the ceiling checked before any policy.\n" +
			"Add it and re-run (--scope replaces the whole list, so name every scope you keep; omit SESSION_ID to use the active session):\n" +
			"  alpacon work-session update [SESSION_ID] --scope command,sudo\n" +
			"Adding a scope may require approval before it takes effect. A new session created with --sudo gets the scope on its own.\n",
	},
	{
		// A Command with no requesting user, i.e. a service-token submission,
		// which the token lane only resolves under EXEC_SUDO_MODE=enforce.
		// Re-running it with the same token repeats it.
		code: "SUDO_COMMAND_NOT_AUTHORIZED",
		guidance: "sudo was denied: this command names no accountable user, so it cannot be elevated—a service token is not one on this deployment.\n" +
			"Sign in with " + utils.LoginWithoutTokenHint + ", then re-run the command under that login.\n",
	},
	{
		code: "SUDO_NO_WORKSESSION_POLICY",
		guidance: "sudo was denied: this command is not covered by a session-authorized sudo policy in your work session.\n" +
			"Add it and re-run (omit SESSION_ID to use the active session):\n" +
			"  alpacon work-session update [SESSION_ID] --sudo \"<command>\"\n" +
			"The addition may require approval before it takes effect.\n",
	},
	{
		// An MFA step-up does not resolve this one—only a session-authorized
		// sudo policy does—so it stays out of the step-up path.
		code: "SUDO_POLICY_MFA_REQUIRED",
		guidance: "sudo was denied: a policy covers this command but requires MFA, which a non-interactive command cannot complete.\n" +
			"Cover it with a session-authorized sudo policy and re-run (omit SESSION_ID to use the active session):\n" +
			"  alpacon work-session update [SESSION_ID] --sudo \"<command>\"\n" +
			"The addition may require approval before it takes effect.\n",
	},
	{
		code:     sudoPresenceRequiredCode,
		guidance: "sudo needs a recent MFA: complete a step-up, then re-run the command.\n",
	},
	{
		code:            "SUDO_APPROVAL_REQUIRED",
		guidance:        "sudo needs approval: an approval request was created. Re-run after a reviewer approves it.\n",
		pendingApproval: true,
	},
	{
		// The session title and description both ride in the risk payload the
		// judge reads, but only the title is a way around the wait: a
		// description edit on an approved/active session is queued for an
		// approval of its own. The hint says "may" because an
		// approval-bypassing principal escapes that queue.
		code: "SUDO_INTENT_DEVIATION",
		guidance: "sudo needs approval: this command reads as off-purpose for your work session, so an approval request was created.\n" +
			"If the session's stated purpose is out of date, re-declare it and re-run instead of waiting for a reviewer (omit SESSION_ID to use the active session):\n" +
			"  alpacon work-session update [SESSION_ID] --title \"<what you are doing>\"\n" +
			"Editing the description instead is not a way around the wait: on an approved or active session that edit may need an approval of its own.\n",
		pendingApproval: true,
		selfService:     true,
	},
	{
		code:     "SUDO_RISK_DENIED",
		guidance: "sudo was denied by runtime risk assessment; this command is not permitted as submitted.\n",
	},
}

// Invocation names the command the user ran, so a hint can show an example
// they can copy without translating it first. A defined type rather than a
// bare string, so an arbitrary string variable cannot reach it without a
// conversion that names the intent. Websh command mode reaches this package
// through RemoteExecArgs.InvokedAs; the zero value renders as exec.
type Invocation string

// approvalOutcome is what the wait loop reads off a command's sudo grant
// status: keep waiting, take the grant, or stop because none is coming.
type approvalOutcome int

// commandRun submits one command and streams it to completion. The retry,
// presence step-up and approval-wait layers take one so they serve the generic
// lane and the verified file lane alike: the lane is decided once, by whoever
// builds the closure, and a re-run after MFA or approval is the same
// closure called again.
type commandRun func() error

// approvalOutcomeOf reads a command detail's sudo grant status into a decision
// the wait loop can switch on. authorized is the only grant; rejected and
// expired are both settled without one. Everything else—an in-progress status,
// an empty string, or the field's absence on an older server—keeps waiting.
func approvalOutcomeOf(status *string) approvalOutcome {
	if status == nil {
		return outcomePending
	}
	switch *status {
	case "authorized":
		return outcomeApproved
	case "rejected":
		return outcomeRejected
	case "expired":
		return outcomeExpired
	default:
		return outcomePending
	}
}

// denialCodePresent reports whether output contains the plugin's terminal denial
// line for the given code. It anchors on the full "Alpacon denied this sudo
// command (CODE)." line—including the trailing period the plugin emits—never a
// bare "(CODE)" token, so a command whose own output prints the token cannot
// forge a match on a command that succeeded. Every code-specific detector routes
// through here, and firstDenialCode scans for the same sudoDenialLinePrefix, so
// the two cannot disagree on what the line looks like.
func denialCodePresent(output, code string) bool {
	return strings.Contains(output, sudoDenialLinePrefix+code+").")
}

// serverHintFollows reports whether the line after an occurrence of
// denialLine in output is the server's hint, which the sudo plugin prints
// right under the denial line when the server sent one. A line counts when it
// has the shape the plugin's sanitizer leaves it in: non-empty, at most
// utils.MaxServerHintBytes, and free of control characters. Its wording and
// punctuation are the server's and are not checked. Another denial line does
// not count (one command line can run several sudo calls), and neither does
// sudo's own error, which follows the denial line on a host where the PAM
// module, not the plugin, decided.
//
// From a server that sends no hint, the command's own output landing on that
// line passes for one and withholds the local sentence. That is accepted: it
// costs only guidance, never a step-up or a wait, since nothing else is keyed
// on this.
func serverHintFollows(output, denialLine string) bool {
	rest := output
	for {
		_, after, found := strings.Cut(rest, denialLine)
		if !found {
			return false
		}
		rest = after
		_, next, ok := strings.Cut(after, "\n")
		if !ok {
			continue
		}
		line, _, _ := strings.Cut(next, "\n")
		if isServerHintLine(strings.TrimSuffix(line, "\r")) {
			return true
		}
	}
}

// isServerHintLine reports whether line has the shape serverHintFollows
// accepts as the server's hint.
func isServerHintLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || len(trimmed) > utils.MaxServerHintBytes {
		return false
	}
	if utils.SanitizeTerminalText(trimmed) != trimmed {
		return false
	}
	// Every line the plugin opens with this words a denial, coded or not.
	if strings.HasPrefix(trimmed, strings.TrimSuffix(sudoDenialCodelessLine, ".")) {
		return false
	}
	return !strings.HasPrefix(trimmed, "sudo:") && !strings.HasPrefix(trimmed, "sudo-rs:")
}

// denialHintLine labels guidance—a table entry's or the unknown-code
// fallback's—as the user-facing hint.
func denialHintLine(guidance string) string {
	return fmt.Sprintf("%s %s", utils.Yellow("Hint:"), guidance)
}

// firstDenialCode returns the code carried by the first well-formed terminal
// denial line in output, or "" when output holds none. It accepts only the
// [A-Z0-9_] shape that the plugin's sanitizer emits, capped at the 63 chars its
// buffer holds, so the code it hands back can be printed verbatim—a
// command's own output cannot smuggle escapes or newlines into a hint through
// it. It answers for known and unknown codes alike; only sudoDenialHint, which
// consults the table first, cares about the difference.
func firstDenialCode(output string) string {
	const maxCodeLen = 63 // the plugin's code buffer holds 64 bytes, NUL included
	rest := output
	for {
		_, after, found := strings.Cut(rest, sudoDenialLinePrefix)
		if !found {
			return ""
		}
		rest = after
		// Keep scanning past a malformed slot rather than giving up on the
		// output: a command that prints a look-alike line of its own would
		// otherwise suppress the hint for the real denial that follows it.
		code, _, closed := strings.Cut(rest, ").")
		if closed && len(code) <= maxCodeLen && isSanitizedDenialCode(code) {
			return code
		}
	}
}

// isSanitizedDenialCode reports whether code has the [A-Z0-9_] shape
// that the plugin's sanitizer emits. Anything else in that slot came from the
// command's own output, so it must never be echoed back into a hint.
func isSanitizedDenialCode(code string) bool {
	if code == "" {
		return false
	}
	for _, r := range code {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

// sudoDenialHint returns actionable guidance when the command output shows a
// non-interactive sudo denial. Returns "" when no such denial is present.
//
// A code with no table entry still gets a hint naming it: the server adds codes
// on its own release train and nothing enforces this table's sync with the
// server's codes, so returning "" for one would leave the next drift invisible
// until someone reported a bare denial line.
//
// A denial carrying no code gets a last, thinner hint: without a category from
// the server there is only the console to point at.
//
// Whichever line answers, a server hint under it (serverHintFollows) already
// said what to do, so the answer is "" then.
func sudoDenialHint(output string) string {
	for _, h := range sudoDenialHints {
		if denialCodePresent(output, h.code) {
			if serverHintFollows(output, sudoDenialLinePrefix+h.code+").") {
				return ""
			}
			return denialHintLine(h.guidance)
		}
	}
	if code := firstDenialCode(output); code != "" {
		if serverHintFollows(output, sudoDenialLinePrefix+code+").") {
			return ""
		}
		return denialHintLine(fmt.Sprintf(
			"sudo was denied (%s). This build carries no guidance for that code—the server may be newer than the CLI.\n"+
				"Read the denial in the Alpacon console (web), and update the CLI so a later run explains it.\n", code))
	}
	// No code slot to anchor on, so a command printing the same sentence in its own
	// output gets the hint too. Accepted: this branch only prints a fixed line—a
	// forged match reaches no step-up, no --wait loop, and echoes nothing the
	// command chose. Anchoring harder re-opens the silent denial this closes.
	if strings.Contains(output, sudoDenialCodelessLine) {
		if serverHintFollows(output, sudoDenialCodelessLine) {
			return ""
		}
		return denialHintLine(
			"sudo was denied, and the denial carried no code to explain it.\n" +
				"Read the denial in the Alpacon console (web).\n")
	}
	return ""
}

// hasSudoPresenceDenial reports whether output carries the non-interactive sudo
// presence denial (SUDO_PRESENCE_REQUIRED), the only denial the CLI can resolve
// in-flow via an MFA step-up.
func hasSudoPresenceDenial(output string) bool {
	return denialCodePresent(output, sudoPresenceRequiredCode)
}

// hasSudoApprovalDenial reports whether output carries a denial that left an
// approval request in flight (the pendingApproval codes in sudoDenialHints): a
// human must approve it out of band in the Alpacon console before the command
// can run. Like the other detectors it anchors on the plugin's exact terminal
// denial line via denialCodePresent, so a command that merely prints the token
// in its own output cannot forge a pending signal or wedge --wait into an
// indefinite re-run loop.
func hasSudoApprovalDenial(output string) bool {
	_, pending := pendingSudoDenial(output)
	return pending
}

// pendingSudoDenial reports whether output carries a pendingApproval code, and
// returns that code's guidance only when the entry is selfService—the caller's
// own pending message already tells the user to re-run after approval, so an
// entry without a way past the wait would just say it twice.
//
// It scans the pendingApproval codes alone rather than deferring to
// sudoDenialHint: one command line can run several sudo calls (`sudo a; sudo b`)
// and carry a denial line for each, and sudoDenialHint answers with whichever
// code sits earliest in the table—which on that output may be a terminal code
// that had no part in the pending classification.
//
// A selfService entry wins over any other pendingApproval code on that same
// output, whatever the table order: the classification is identical either way,
// so answering with the entry that has no way past the wait would drop the only
// guidance the user could act on.
//
// A server hint under that entry's denial line withholds the guidance as in
// sudoDenialHint; pending is unaffected, since it decides the wait, not a
// sentence.
func pendingSudoDenial(output string) (hint string, pending bool) {
	for _, h := range sudoDenialHints {
		if h.pendingApproval && h.selfService && denialCodePresent(output, h.code) {
			if serverHintFollows(output, sudoDenialLinePrefix+h.code+").") {
				return "", true
			}
			return denialHintLine(h.guidance), true
		}
	}
	for _, h := range sudoDenialHints {
		if h.pendingApproval && denialCodePresent(output, h.code) {
			return "", true
		}
	}
	return "", false
}

// isCommandInlineCredentialError reports whether err carries the server's
// inline-credential gate code (utils.CommandInlineCredential): the submitted
// content itself contained a credential (e.g. a -p/--password flag, a
// KEY=VALUE secret such as PGPASSWORD=..., or a user:pass@host connection
// string), so the server refused it before it ever ran rather than persist it.
// The content is the command line on the exec and websh lanes and the verified
// file's content on the --file lane.
func isCommandInlineCredentialError(err error) bool {
	code, _ := utils.ParseErrorResponse(err)
	return code == utils.CommandInlineCredential
}

// credentialInlineExample renders the --env line for invokedAs. The two commands
// take the remote command differently: only exec has the -- separator
// (cmd/exec/parse.go), while websh takes it as one quoted argument.
func credentialInlineExample(invokedAs Invocation) string {
	if invokedAs == WebshInvocation {
		return string(WebshInvocation) + ` --env="SECRET_NAME" db-server '<command>'`
	}
	return string(ExecInvocation) + ` --env="SECRET_NAME" db-server -- <command>`
}

// credentialInlineHint returns the actionable guidance printed alongside
// commandInlineCredentialMessage. It never echoes the rejected command
// line—only fixed guidance naming --env—so it cannot leak the credential it is
// warning about. invokedAs picks the example; empty falls back to exec.
func credentialInlineHint(invokedAs Invocation) string {
	return fmt.Sprintf(
		"%s move the secret to --env instead (its value is read from your shell, so it never lands on the command line the server stores):\n"+
			"  %s\n",
		utils.Yellow("Hint:"), credentialInlineExample(invokedAs))
}

// RunExecWithPresenceStepUp runs a command via RunCommandWithRetry and, when it
// is denied for a missing recent MFA (SUDO_PRESENCE_REQUIRED) on an interactive
// terminal, offers an MFA step-up and retries once. Non-interactive callers
// (scripts, CI, AI agents) fall through unchanged so HandleCommandResult prints
// the static denial hint; non-interactive humans additionally get the
// verification link they can complete out of band. Reached via RunRemoteExec by
// exec and websh command mode; interactive websh keeps its own sudo MFA flow.
func RunExecWithPresenceStepUp(ac *client.AlpaconClient, serverName, command, username, groupname string, env map[string]string, workSessionID, purpose string, out io.Writer) error {
	return runWithPresenceStepUp(ac, serverName, func() error {
		return RunCommandWithRetry(ac, serverName, command, username, groupname, env, workSessionID, purpose, out)
	})
}

// runWithPresenceStepUp is RunExecWithPresenceStepUp over a commandRun.
func runWithPresenceStepUp(ac *client.AlpaconClient, serverName string, run commandRun) error {
	err := run()
	// A real presence denial makes sudo exit non-zero, so it always surfaces as a
	// RemoteCommandError carrying the denial line. Require that error as well as
	// the line match: a command that merely prints the line and SUCCEEDS
	// (err == nil) must not trigger a step-up and a re-run of a side-effecting
	// command.
	var remoteErr *event.RemoteCommandError
	if !errors.As(err, &remoteErr) || !hasSudoPresenceDenial(remoteErr.Output) {
		return err
	}

	if !utils.IsInteractiveShell() {
		printPresenceStepUpLink(ac, serverName)
		return err
	}

	if stepErr := mfa.StepUpForSudo(ac, serverName); stepErr != nil {
		utils.CliWarning("MFA step-up did not complete: %s", stepErr)
		return err
	}

	// Presence is fresh—retry once. Any remaining denial falls through to the
	// static hint in HandleCommandResult.
	return run()
}

// printPresenceStepUpLink surfaces the verification link for a non-interactive
// presence denial, so a human reading the logs can complete MFA out of band and
// re-run. We cannot prompt or open a browser here. A link that cannot be
// fetched is dropped silently: the static denial hint already carries the
// actionable part, and the link is the extra.
func printPresenceStepUpLink(ac *client.AlpaconClient, serverName string) {
	url, err := mfaLinkByServerName(ac, serverName)
	if err != nil {
		return
	}
	utils.CliInfo("MFA verification link (open in a browser):\n  %s", url)
}

// approvalDenialOutput returns the captured output of a denial that left an
// approval request in flight: the command exited non-zero (a real denial always
// surfaces as a RemoteCommandError) AND the plugin's exact denial line for a
// pendingApproval code is in that output. Requiring both keeps a command that
// merely prints the token but succeeds (err == nil) from being mistaken for a
// pending approval. It hands back the output, not a bool, so a caller needing
// the denial code does not restate the rule for what counts as one.
func approvalDenialOutput(err error) (string, bool) {
	var remoteErr *event.RemoteCommandError
	if !errors.As(err, &remoteErr) || !hasSudoApprovalDenial(remoteErr.Output) {
		return "", false
	}
	return remoteErr.Output, true
}

// isApprovalDenial reports whether err is a denial with an approval request in flight.
func isApprovalDenial(err error) bool {
	_, ok := approvalDenialOutput(err)
	return ok
}

// pollApprovalOnce reads the grant the denial left behind. The command it names
// already ran and was blocked, so this is a status read, not a re-attempt.
func pollApprovalOnce(ac *client.AlpaconClient, cmdID string) (approvalOutcome, string, error) {
	details, err := getCommandByID(ac, cmdID)
	if err != nil {
		return outcomePending, "", err
	}
	requestID := ""
	if details.SudoApprovalRequestID != nil {
		requestID = *details.SudoApprovalRequestID
	}
	return approvalOutcomeOf(details.SudoGrantStatus), requestID, nil
}

// RunExecWithApprovalWait runs a command via RunExecWithPresenceStepUp and, when
// it is denied with an approval request in flight (a pendingApproval code) and
// waitTimeout is positive, blocks and polls the command's detail until a
// reviewer grants or refuses the request out of band, or the bounded timeout
// elapses. When waitTimeout is zero or negative, or the denial carries a
// terminal code, it returns the first err unchanged so the caller's
// pending/denial handling runs.
//
// A tick that never reached the server is not an answer: the loop backs off, and
// a timeout—or a run of failed polls long enough to give up on—reports the denial
// that opened the wait, so the caller still exits on the pending contract rather
// than as a generic failure.
//
// The poll reads the command's own detail (sudo_grant_status) rather than
// re-submitting it: the denial carries the command id, and re-attempting a
// command still pending approval would file a fresh approval request on every
// tick. The poll mirrors the MFA step-up structure (api/mfa/mfa.go): a spinner,
// a timer, and a precise deadline.
func RunExecWithApprovalWait(ac *client.AlpaconClient, serverName, command, username, groupname string, env map[string]string, workSessionID, purpose string, waitTimeout time.Duration, out io.Writer) error {
	return runWithApprovalWait(ac, func() error {
		return runPresenceStepUp(ac, serverName, command, username, groupname, env, workSessionID, purpose, out)
	}, waitTimeout, out)
}

// RunFileExecWithApprovalWait is RunExecWithApprovalWait for the verified file
// lane. Only the submission differs—the file object in place of a command
// line—so the presence step-up, the approval wait and the re-run after a grant
// are the same layers the generic lane runs through.
func RunFileExecWithApprovalWait(ac *client.AlpaconClient, serverName string, file event.FileExecution, username, groupname, workSessionID, purpose string, waitTimeout time.Duration, out io.Writer) error {
	return runWithApprovalWait(ac, func() error {
		return runWithPresenceStepUp(ac, serverName, func() error {
			// One stream for the attempt and its MFA retries; see RunCommandWithRetry.
			stream := event.NewCommandStream(ac)
			defer stream.Close()
			return runWithRetry(ac, serverName, func() error {
				return stream.RunFileCommand(serverName, file, username, groupname, workSessionID, purpose, out)
			})
		})
	}, waitTimeout, out)
}

// runWithApprovalWait is RunExecWithApprovalWait over a commandRun: run is the
// first attempt and, once a reviewer grants the request, the re-run.
func runWithApprovalWait(ac *client.AlpaconClient, run commandRun, waitTimeout time.Duration, out io.Writer) error {
	err := run()

	// Status-hold: the server parked this job at awaiting_approval (it never ran).
	// With --wait, resubscribe to the same job and stream once approved instead of
	// re-submitting; without --wait, surface it for HandlePendingApproval.
	var pendingErr *event.PendingApprovalError
	if errors.As(err, &pendingErr) {
		if waitTimeout <= 0 {
			return err
		}
		spinner := utils.NewSpinner("Waiting for approval in the Alpacon console (output streams once approved)...")
		spinner.Start()
		defer spinner.Stop()
		// The wait is inside the stream call, so the spinner has to survive it—
		// StopWriter retires it the moment the approved command's output starts.
		return streamApprovedCommand(ac, pendingErr.CommandID, spinner.StopWriter(out), waitTimeout)
	}

	if waitTimeout <= 0 || !isApprovalDenial(err) {
		return err
	}

	var denialErr *event.RemoteCommandError
	if !errors.As(err, &denialErr) || denialErr.CommandID == "" {
		// Nothing to poll: fall back to reporting the denial as it stands.
		return err
	}
	cmdID := denialErr.CommandID

	spinner := utils.NewSpinner(approvalWaitMessage)
	spinner.Start()
	// Every return path below stops the spinner before it writes, which is
	// load-bearing for output ordering; this backstops paths added later. Stop
	// on a stopped spinner is a no-op.
	defer spinner.Stop()

	pendingDenial := err
	started := time.Now()
	deadline := started.Add(waitTimeout)
	timer := time.NewTimer(waitTimeout)
	defer timer.Stop()
	poll := time.NewTimer(approvalWaitPollInterval)
	defer poll.Stop()
	failures := 0
	throttles := 0
	lastRequestID := ""
	budget := utils.NewThrottleBudget(waitTimeout)
	// lastRefusal is the refusal (a 429 in practice) the latest status read got,
	// if it got one, so a wait that times out on it still prints its hint.
	var lastRefusal error
	for {
		select {
		case <-timer.C:
			spinner.Stop()
			// Report only the timeout; the caller's pending-approval message already names --wait-approval.
			utils.CliWarning("%s", utils.HintOnly(fmt.Sprintf("Approval wait timed out after %s; the command is still pending.", waitTimeout), lastRefusal))
			return pendingWithRequestID(pendingDenial, lastRequestID)
		case <-poll.C:
			outcome, requestID, err := pollApprovalOnce(ac, cmdID)
			lastRefusal = nil
			if err != nil && utils.IsRefusalStatus(utils.HTTPStatusCode(err)) {
				lastRefusal = err
			}
			switch {
			case err != nil && isPollFailure(err):
				// A 429 says the server is answering, so it is not counted toward
				// the cap below—the throttle budget and the deadline bound it
				// instead, and counting it would end the wait before the budget it
				// just spent could carry it anywhere.
				if utils.HTTPStatusCode(err) == http.StatusTooManyRequests {
					delay := utils.NextPollBackoff(approvalWaitPollInterval, throttles, utils.RetryAfter(err))
					throttles++
					budget.WarnThrottled(delay)
					if newDeadline, extended := budget.Extend(deadline, delay); extended {
						deadline = newDeadline
						// No drain before Reset: under this module's Go 1.23+ timer
						// semantics the channel is unbuffered and a receive after Stop
						// is guaranteed to block, so the drain could only hang.
						timer.Stop()
						timer.Reset(time.Until(deadline))
					}
					poll.Reset(delay)
					continue
				}
				failures++
				if failures >= utils.MaxConsecutivePollFailures {
					spinner.Stop()
					// The approval request is still open, so this exits on the
					// pending contract like the timeout does. Exit 1 reads as
					// retryable, and an agent answers it by re-running exec and
					// filing a second request for the same command.
					utils.CliWarning("Approval wait gave up after %d failed polls (%s); the command is still pending.", failures, err)
					return pendingWithRequestID(pendingDenial, lastRequestID)
				}
				poll.Reset(utils.NextPollBackoff(approvalWaitPollInterval, failures-1, utils.RetryAfter(err)))
				continue
			case err != nil:
				spinner.Stop()
				// A fatal 4xx on the status read says nothing about the approval
				// request, which is still open—so this exits on the pending contract
				// like the give-up branch above, rather than on an exit 1 an agent
				// answers by filing a second request for the same command.
				utils.CliWarning("Approval status read failed (%s); the command is still pending.", err)
				return pendingWithRequestID(pendingDenial, lastRequestID)
			}
			failures = 0
			throttles = 0
			// The budget is not reset here: reading a grant that is still pending is
			// not progress, and this loop returns the moment the status becomes one.
			// So the allowance runs for the life of the wait, like the deadline it
			// extends—resetting per poll would re-earn it on any non-429 response.
			if requestID != "" {
				lastRequestID = requestID
			}
			switch outcome {
			case outcomeApproved:
				spinner.Stop()
				return runAfterApproval(run)
			case outcomeRejected, outcomeExpired:
				spinner.Stop()
				// Settled without a grant. CommandRejectedError is what already carries
				// this to ExitCodeNotApproved, so the two denial shapes exit alike; only
				// the sentence they print differs.
				return &event.CommandRejectedError{CommandID: cmdID, Expired: outcome == outcomeExpired}
			}
			// A fixed gap over a 30m wait outspends the server's service-token
			// throttle, so the gap widens as the wait ages.
			poll.Reset(utils.NextPollTick(approvalWaitPollInterval, time.Since(started)))
		}
	}
}

// runAfterApproval runs the command once the grant is authorized. A denial here
// means the grant went somewhere else—it expired, or another attempt spent
// it—so this reports that instead of opening a second wait the user never
// asked for.
func runAfterApproval(run commandRun) error {
	err := run()
	// Either shape means the grant did not carry this run, and each gets the
	// sentence its own way out needs. A repeated denial is answered by filing a
	// fresh request; a job the server parked runs on its own once approved, so
	// telling that caller to re-run would contradict the pending text
	// HandlePendingApproval prints moments later on the same stream.
	var pendingErr *event.PendingApprovalError
	switch {
	case isApprovalDenial(err):
		utils.CliWarning("The approval was granted but the command was denied again; the grant appears already used or expired. Re-run the command to request approval again.")
	case errors.As(err, &pendingErr):
		utils.CliWarning("The approval was granted but the grant did not carry this run; the server is holding the command for a fresh approval.")
	}
	return err
}

// pendingWithRequestID copies the denial so the wait can attach what it learned
// while polling. The original is the caller's; mutating it would surprise them.
func pendingWithRequestID(denial error, requestID string) error {
	var remoteErr *event.RemoteCommandError
	if requestID == "" || !errors.As(denial, &remoteErr) {
		return denial
	}
	withID := *remoteErr
	withID.ApprovalRequestID = requestID
	return &withID
}

// isPollFailure separates a status read that carried no usable answer from one
// that did: a network error, an undecodable body, or a retryable HTTP status
// (any but a fatal 4xx, so 429/5xx keep the wait alive). It costs nothing to be
// liberal here—the poll only reads the command detail, so a false retry wastes a
// tick, never a re-run of the command. A run of them is capped at
// MaxConsecutivePollFailures, except for 429s: the throttle budget bounds those.
//
// *url.Error satisfies net.Error, so one errors.As covers both the dial and the
// round-trip failure—neither reached the server. *json.SyntaxError and
// *json.UnmarshalTypeError cover a body that did reach it but would not decode
// (a proxy error page under a JSON content type, or a response shape that
// drifted).
func isPollFailure(err error) bool {
	if err == nil {
		return false
	}
	if status := utils.HTTPStatusCode(err); status != 0 {
		return !utils.IsFatalClientError(status)
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return true
	}
	var typeErr *json.UnmarshalTypeError
	return errors.As(err, &typeErr)
}

// HandlePurposeDemand reports a command the gate parked for its purpose and
// exits, or returns false when err is something else.
//
// It never waits, not even under --wait. --wait exists because an approval is
// somebody else's to give and the caller can only block; a purpose demand is
// this caller's own to answer, and the window is about a minute, so blocking
// would spend the command's single chance on sleep. It runs ahead of
// HandlePendingApproval because a parked command has no approval request yet:
// reporting one would name a queue nobody has been added to.
func HandlePurposeDemand(err error) bool {
	var purposeErr *event.AwaitingPurposeError
	if !errors.As(err, &purposeErr) {
		return false
	}
	utils.PrintPurposeDemand(
		utils.PurposeDemandLead, purposeErr.CommandID, purposeErr.ExpiresAt,
	)
	os.Exit(utils.ExitCodePurposeRequired)
	return true
}

// HandlePendingApproval emits the structured pending-approval feedback for a
// command left pending human approval and not waited on—either a job the server
// parked at awaiting_approval (PendingApprovalError) or a sudo denial with an
// approval request in flight—then exits with ExitCodePendingApproval. It reports
// true when it handled the err; the caller skips its normal result handling on
// true. The status-hold path carries no approval request id; the sudo-denial
// path carries one only when a --wait poll picked one up along the way
// (pendingWithRequestID), so the machine signal reports an id when it has one
// and omits it otherwise. The sudo-denial path repeats the exact command the
// caller invoked (reRunHint of parsed), so a human can copy-paste it once the
// request is approved. A held command line the server reads as a script on the
// target also gets the --file --from-server form of it (fileSuggestionAction).
func HandlePendingApproval(err error, parsed RemoteExecArgs) bool {
	// Status-hold: held job runs automatically once approved, so point at exec logs.
	var pendingErr *event.PendingApprovalError
	if errors.As(err, &pendingErr) {
		var extra []utils.NextAction
		if action, ok := fileSuggestionAction(parsed, pendingErr.FileSuggestion); ok {
			extra = append(extra, action)
		}
		utils.PrintPendingApproval(
			"Approval required—this command is held for human approval in the Alpacon console (web). "+
				"It runs automatically once approved; pass --wait (or --wait-approval DURATION for a longer wait) to block until then.",
			"", // the command detail carries no approval request id
			utils.NextAction{Command: fmt.Sprintf("alpacon exec logs %s", pendingErr.CommandID)},
			extra...,
		)
		os.Exit(utils.ExitCodePendingApproval)
		return true
	}
	output, ok := approvalDenialOutput(err)
	if !ok {
		return false
	}
	// This exits before HandleCommandResult, the hint's only other caller, so a
	// self-service path out (re-declaring a stale session intent) would
	// otherwise never reach the user. pendingSudoDenial withholds the guidance
	// for codes whose only way out is the wait the message below already names.
	if hint, _ := pendingSudoDenial(output); hint != "" {
		fmt.Fprint(os.Stderr, hint)
	}
	requestID := ""
	var remoteErr *event.RemoteCommandError
	if errors.As(err, &remoteErr) {
		requestID = remoteErr.ApprovalRequestID
	}
	utils.PrintPendingApproval(
		"Approval required—a human must approve this sudo command in the Alpacon console (web). "+
			"Re-run after approval, or use --wait (or --wait-approval DURATION for a longer wait) to block until it is approved.",
		requestID,
		reRunHint(parsed),
	)
	os.Exit(utils.ExitCodePendingApproval)
	return true
}

// RunCommandWithRetry executes a remote command with MFA/username-required error
// handling and retry logic, streaming output to out.
// workSessionID is forwarded as the work_session field; pass "" to omit it.
//
// The attempt and its retries share one output stream: the MFA wait retries a
// refused submission once a second for minutes, and a stream per attempt would
// open an event session and a WebSocket each time.
func RunCommandWithRetry(ac *client.AlpaconClient, serverName, command, username, groupname string, env map[string]string, workSessionID, purpose string, out io.Writer) error {
	stream := event.NewCommandStream(ac)
	defer stream.Close()
	return runWithRetry(ac, serverName, func() error {
		return stream.RunCommand(serverName, command, username, groupname, env, workSessionID, purpose, out)
	})
}

// runWithRetry is RunCommandWithRetry over a commandRun; the MFA retry is the
// same closure called again.
func runWithRetry(ac *client.AlpaconClient, serverName string, run commandRun) error {
	err := run()
	if propagated, ok := propagateCommandError(err); ok {
		return propagated
	}
	if err != nil {
		err = utils.HandleCommonErrors(err, serverName, mfa.ErrorCallbacks(ac, run))
		// RetryOperation may surface a propagated error; re-check after HandleCommonErrors.
		if propagated, ok := propagateCommandError(err); ok {
			return propagated
		}
		if err != nil {
			return fmt.Errorf("failed to execute command on '%s' server: %w", serverName, err)
		}
	}
	return nil
}

// HandleCommandResult exits appropriately on error. Output is streamed to stdout
// during execution; on a remote failure the error carries that output, used here
// only to surface the sudo-denial hint (not re-printed). invokedAs names the
// command the user ran so a hint can quote it; empty falls back to exec.
func HandleCommandResult(err error, invokedAs Invocation) {
	if err != nil {
		var remoteErr *event.RemoteCommandError
		if errors.As(err, &remoteErr) {
			stderrText, exitCode := remoteFailureReport(remoteErr)
			fmt.Fprint(os.Stderr, stderrText)
			os.Exit(exitCode)
		}
		var clientTimeout *event.ClientTimeoutError
		if errors.As(err, &clientTimeout) {
			fmt.Fprint(os.Stderr, clientTimeoutLine())
			// A wait that ended on a refusal (a 429 in practice) keeps its hint.
			utils.PrintRefusalHints(os.Stderr, clientTimeout)
			os.Exit(1)
		}
		var rejected *event.CommandRejectedError
		if errors.As(err, &rejected) {
			// Settled without a grant: retrying only files another approval request.
			utils.CliErrorEnvelopeWithExitCode(utils.ExitCodeNotApproved, "command", err, "%s", rejected)
			return
		}
		if isCommandInlineCredentialError(err) {
			reportCodedRefusal("command", err, commandInlineCredentialMessage, credentialInlineHint(invokedAs))
			return
		}
		utils.CliErrorWithExit("%s", err)
	}
}

// remoteFailureReport is what HandleCommandResult writes to stderr for a
// command that failed on the server, and the exit code it leaves with: the
// outcome line, then the sudo denial guidance when the output needs one.
func remoteFailureReport(remoteErr *event.RemoteCommandError) (string, int) {
	stderrLine, exitCode := remoteCommandOutcome(remoteErr)
	return stderrLine + sudoDenialHint(remoteErr.Output), exitCode
}

// reportCodedRefusal is the one way out for a server refusal the CLI has a
// sentence for: under --output json the envelope carries the server's code and
// the message; table mode prints the red Error line and the hint. Exits 1.
// The server's hint, when err carries one, takes the place of the local hint.
func reportCodedRefusal(operation string, err error, message, hint string) {
	reportCodedRefusalWithExit(1, operation, err, message, hint, nil)
}

// reportCodedRefusalWithExit is reportCodedRefusal with a chosen exit code and
// the next_actions the JSON envelope carries; table mode shows only the hint,
// so it should spell out the same actions for a person. Under --output json an
// ErrInvalidID refusal goes out as a usage error with exit 2, so the envelope's
// code and the exit code agree.
func reportCodedRefusalWithExit(code int, operation string, err error, message, hint string, actions []utils.NextAction) {
	if utils.OutputFormat == utils.OutputFormatJSON && errors.Is(err, utils.ErrInvalidID) {
		utils.CliUsageErrorEnvelopeWithExit(operation, "%s.", message)
		return
	}
	utils.ExitIfUsageError(code, operation, err, "%s.", message)
	utils.ExitWithFailureJSON(utils.Failure{ExitCode: code, Operation: operation, Err: err, Message: message + ".", NextActions: actions})
	fmt.Fprintf(os.Stderr, "%s: %s.\n", utils.Red("Error"), message)
	if hint := codedRefusalHint(err, hint); hint != "" {
		fmt.Fprint(os.Stderr, hint)
	}
	os.Exit(code)
}

// codedRefusalHint is the hint reportCodedRefusal prints: the server's, when
// err carries one, otherwise the local one it was given.
func codedRefusalHint(err error, local string) string {
	if serverHint := utils.RefusalHint(err); serverHint != "" {
		return denialHintLine(serverHint + "\n")
	}
	return local
}

// propagateCommandError reports errors RunCommandWithRetry must return unchanged
// (never MFA-retried or wrapped): phased errors and a status-hold PendingApprovalError.
func propagateCommandError(err error) (error, bool) {
	if phased, ok := asPhasedError(err); ok {
		return phased, true
	}
	var pending *event.PendingApprovalError
	if errors.As(err, &pending) {
		return pending, true
	}
	// The purpose demand is the caller's to answer, so it must reach
	// HandlePurposeDemand intact rather than be retried as a transport failure.
	var purposeDemand *event.AwaitingPurposeError
	if errors.As(err, &purposeDemand) {
		return purposeDemand, true
	}
	return nil, false
}

// asPhasedError unwraps err to a RemoteCommandError or ClientTimeoutError.
func asPhasedError(err error) (error, bool) {
	if err == nil {
		return nil, false
	}
	var remoteErr *event.RemoteCommandError
	if errors.As(err, &remoteErr) {
		return remoteErr, true
	}
	var clientTimeout *event.ClientTimeoutError
	if errors.As(err, &clientTimeout) {
		return clientTimeout, true
	}
	return nil, false
}

// clientTimeoutLine renders the stderr line for ClientTimeoutError (with newline).
func clientTimeoutLine() string {
	const phase = "client_timeout"
	return fmt.Sprintf("%s: [%s] %s\n", utils.Red("Error"), phase, event.DescribePhase(phase))
}

// detachResultLines renders the --detach confirmation. The job id comes from the
// server's submit response and both lines are written raw, outside the Cli*
// helpers—sanitize here (#364).
func detachResultLines(jobID string) (string, string) {
	jobID = utils.SanitizeTerminalText(jobID)
	return fmt.Sprintf("Job submitted: %s", jobID),
		fmt.Sprintf("Run `alpacon exec logs %s` to check the result.", jobID)
}

// sanitizedPhaseParts returns the phase identifier and its description ready for
// a raw stderr write, outside the Cli* sanitizing helpers (#364). The identifier
// is always the server's; the description is a constant from phaseDescriptions
// for a known phase, and the raw phase echoed back for an unknown one. Describing
// before stripping is what keeps a payload that only becomes a known phase once
// sanitized from borrowing that phase's description.
func sanitizedPhaseParts(phase string) (id, desc string) {
	return utils.SanitizeTerminalText(phase),
		utils.SanitizeTerminalText(event.DescribePhase(phase))
}

// phaseHintLine returns the indented hint line for a phase, including its
// trailing newline, or "" when the phase has no hint.
func phaseHintLine(phase string) string {
	hint := event.PhaseHint(phase)
	if hint == "" {
		return ""
	}
	return fmt.Sprintf("Hint: %s\n", utils.SanitizeTerminalText(hint))
}

// remoteCommandOutcome renders the stderr phase line and exit code for a remote
// command failure. The command's stdout was already streamed during execution,
// so it is not re-emitted here. stderrLine already includes its trailing newline.
func remoteCommandOutcome(remoteErr *event.RemoteCommandError) (stderrLine string, exitCode int) {
	if remoteErr.ErrorPhase != "" {
		phase, desc := sanitizedPhaseParts(remoteErr.ErrorPhase)
		stderrLine = fmt.Sprintf("%s: [%s] %s\n", utils.Red("Error"), phase, desc) +
			phaseHintLine(remoteErr.ErrorPhase)
	}
	return stderrLine, remoteErr.ExitCode
}

// HandleExecUserRefusal reports the missing execution user and exits 1, or
// returns false when err is something else. Under --output json it returns
// false so the envelope stays what it is today.
func HandleExecUserRefusal(err error, parsed RemoteExecArgs) bool {
	if utils.OutputFormat == utils.OutputFormatJSON {
		return false
	}
	message, hint, ok := execUserRefusal(err, parsed)
	if !ok {
		return false
	}
	reportCodedRefusal("command", err, message, hint)
	return true
}

// execUserRefusal renders the refusal for a command sent with no execution user.
// One that named a user or group keeps the server's own error, which this hint would contradict.
func execUserRefusal(err error, parsed RemoteExecArgs) (message, hint string, ok bool) {
	if parsed.Username != "" || parsed.Groupname != "" ||
		utils.StructuredErrorCode(err) != usernameGroupnameInvalidCode {
		return "", "", false
	}
	return fmt.Sprintf("no execution user was resolved for '%s' server", utils.SanitizeTerminalText(parsed.Server)),
		denialHintLine(fmt.Sprintf(execUserRequiredHint, execUserRequiredExamples(parsed.InvokedAs))), true
}

func execUserRequiredExamples(invokedAs Invocation) string {
	if invokedAs == WebshInvocation {
		return "  alpacon websh -u USERNAME SERVER 'COMMAND'\n  alpacon websh USERNAME@SERVER 'COMMAND'\n"
	}
	return "  alpacon exec -u USERNAME SERVER -- COMMAND\n  alpacon exec USERNAME@SERVER -- COMMAND\n"
}
