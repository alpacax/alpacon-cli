# Alpacon CLI

[![Go Version](https://img.shields.io/github/go-mod/go-version/alpacax/alpacon-cli)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://github.com/alpacax/alpacon-cli/blob/main/LICENSE)
[![Latest Release](https://img.shields.io/github/v/release/alpacax/alpacon-cli)](https://github.com/alpacax/alpacon-cli/releases)

`Alpacon CLI` is the command-line client for [Alpacon](https://alpacon.io), the AI-native PAM. With Alpacon, humans, AI agents, and CI/CD pipelines reach and operate your entire fleet through a single identity—and every command they run is judged at runtime, recorded, and bounded by a scoped work session. Three guarantees:

1. **A gate, not a credential.** After login, a **work session** is the first thing required—nothing reaches your servers without one. Sessions are scoped (servers, commands, time window).
2. **Damage containment.** Every command is judged at runtime against the session's scope. If a credential leaks or an AI client is compromised, what the attacker can do is bounded by the session, not by what the credential could touch on its own.
3. **One audit shape.** Everything inside a session is recorded—same timeline whether the actor is human, AI agent, or CI/CD pipeline.

This CLI lets you drive your Alpacon workspace from the terminal: open a work session, then Websh into a server, exec remote commands, transfer files, create TCP tunnels, and manage API tokens with command/server/file ACLs. Login is browser-based (OAuth + MFA); everything else stays in the terminal. Built for engineers, AI coding agents (Claude Code, GitHub Copilot, Cursor, Codex CLI, Gemini CLI), and CI/CD platforms.

## Architecture

- **Alpacon Server**—the AI-native PAM control plane. Web console with simple OAuth + MFA login. Centralized RBAC, runtime command judgment, session recording, and 100% audit. Sign up at [alpacon.io](https://alpacon.io).
- **[Alpamon](https://github.com/alpacax/alpamon)**—open-source agent installed on managed servers. Outbound-only connection (no inbound ports, no firewall changes); enforces server-side decisions locally.
- **Alpacon CLI** (this repository)—command-line client for your Alpacon workspace.

## Documentation

For production usage, see the [official documentation](https://docs.alpacax.com/reference/cli/). This README is the engineering / contribution guide.

## Installation

> [!IMPORTANT]
> Building from source is for development. For production, use the package managers below or pre-built binaries from [Releases](https://github.com/alpacax/alpacon-cli/releases).

### macOS (Homebrew)
```bash
brew install alpacax/alpacon/alpacon-cli
```

### Linux (Debian / Ubuntu)
```bash
curl -s https://packagecloud.io/install/repositories/alpacax/alpacon/script.deb.sh?any=true | sudo bash
sudo apt-get install alpacon
```

### Linux (RHEL / Rocky / AlmaLinux)
```bash
curl -s https://packagecloud.io/install/repositories/alpacax/alpacon/script.rpm.sh?any=true | sudo bash
sudo yum install alpacon
```

### Windows
Download the latest `.zip` from [Releases](https://github.com/alpacax/alpacon-cli/releases) and add the binary to your `PATH`.

### Docker
```bash
docker run --rm -it alpacax/alpacon-cli version
```

### Build from source
```bash
git clone https://github.com/alpacax/alpacon-cli.git
cd alpacon-cli
go build && sudo mv alpacon-cli /usr/local/bin/alpacon
```

## Quick start

```bash
# 1. Check current login + workspace.
#    Run 'alpacon login' or 'alpacon workspace switch' if not logged in or in the wrong place.
$ alpacon

# 2. Confirm identity and whether a work session is required.
$ alpacon whoami

# 3. Open a scoped work session (interactive auth only).
$ alpacon work-session create \
    --purpose "describe the task" \
    --scope command,websh \
    --server <server> \
    --expires-in 1h \
    --use --wait          # --wait-approval 30m waits longer (default 5m)

# 4. Operate within the session.
$ alpacon websh <server>
$ alpacon exec <server> "uptime"
$ alpacon cp ./file.txt <server>:/tmp/
$ alpacon tunnel <server> -l 9000 -r 8082
```

CI/CD and API automation use token auth, which bypasses work sessions:

```bash
$ alpacon login <URL> -t <TOKEN_KEY>
$ alpacon exec <server> "..."
```

See `alpacon work-session --help` for session lifecycle, gating, and error codes.

## Login

```bash
$ alpacon login                                  # browser OAuth (default)
$ alpacon login --workspace my-ws --region us1   # cloud workspace by name/region
$ alpacon login alpacon.example.com              # self-hosted
$ alpacon login <URL> -t <TOKEN_KEY>             # API token
$ alpacon login myws.us1.alpacon.io              # cloud direct URL (deprecated)
$ alpacon login --workspace my-ws --region us1 -t <TOKEN_KEY> # CI / automation
$ alpacon login --workspace my-ws --region us1 --no-browser   # manual login from a headless shell
$ alpacon logout
```

Successful login writes `~/.alpacon/config.json` containing the workspace target and credentials. Browser OAuth stores access/refresh tokens and access-token expiry; `-t` stores the supplied API token. In an interactive shell, re-login prompts with the stored target as the default instead of silently reusing it; non-interactive login requires an explicit host or `--workspace/--region`.

Browser login also sends a device identifier to Auth0 so an MFA prompt can be bound to the installation that requested it. It is a random value generated once and reused by every workspace this installation logs in to. On its own it authenticates nothing—an attacker who knows it still has to sign in as you—but it is the value your MFA verification is bound to, and it names this installation to the identity provider on every login and token refresh, so treat it as identifying rather than harmless: keep it out of logs and bug reports. It is stored in `~/.alpacon/device_id` with owner-only permissions—a separate file from `config.json`, so it survives `alpacon logout`: the identifier describes the machine, not the session, and regenerating it would invalidate MFA verifications already tied to it and prompt you again. Delete the file to reset it; the next login generates a new one.

An installation that logged in before this identifier existed holds a refresh token issued without it. If the identity provider refuses to refresh that token with the identifier attached, the CLI retries the refresh without it, so the session keeps working and MFA verification falls back to the previous behaviour until the next `alpacon login`. Set `ALPACON_DEBUG=1` to see when that retry happens.

For Auth0 and MFA authentication the CLI opens the auth URL in your default browser; this is skipped automatically in SSH sessions and headless environments. To force it off, use `--no-browser` or set `ALPACON_NO_BROWSER=1`. The same env var also suppresses MFA browser prompts triggered by other commands.

## Commands

Run `alpacon --help` for the full command list. Common workflows below.

### Servers
```bash
$ alpacon server ls
$ alpacon server describe <server>
$ alpacon server create                          # interactive: prompts for name,
                                                 # platform (debian/rhel/darwin/windows),
                                                 # and authorized groups
$ alpacon server rm <server>
```

### Websh (terminal in your shell)
```bash
$ alpacon websh <server>
$ alpacon websh root@<server>
$ alpacon websh -u admin -g developers <server>
$ alpacon websh --share <server>                 # share via temporary link
$ alpacon websh join --url <SHARED_URL> --password <PASSWORD>
```

### Remote command execution
```bash
$ alpacon exec <server> "<cmd>"
$ alpacon exec root@<server> "docker ps"
$ alpacon exec -u admin -g developers <server> "..."

# Pass a secret with --env="KEY": the value is read from your shell, so it stays off
# the alpacon command line. Read it in rather than typing it inline, so it stays out
# of shell history too.
$ printf 'PGPASSWORD: ' && read -rs PGPASSWORD && export PGPASSWORD
$ alpacon exec --env="PGPASSWORD" <server> -- psql -h localhost -U app -c 'SELECT 1'
```

Flags go before the server name; everything after is the remote command.

Never put a secret on the command line: the server refuses the recognizable forms before the command runs. Pass it with `--env="KEY"` as shown above. The same applies to `alpacon websh` when it runs a command. See [When a command is denied](#when-a-command-is-denied) for the exact forms the server rejects and the machine-readable refusal.

### File transfer
```bash
$ alpacon cp ./local.txt <server>:/home/user/
$ alpacon cp <server>:/home/user/file.txt .
$ alpacon cp -u admin -g developers <SOURCE> <DESTINATION>
$ alpacon edit <server>:/etc/nginx/nginx.conf    # open a remote file in your local editor
```

`<server>:<path>` denotes a remote target. A file `cp` downloads is created owner-only—`0600` before the umask, which can only narrow it further—since remote files routinely carry secrets. A local file that already exists keeps its current mode instead; for such a single-file download, a warning on stderr says so when that kept mode is group- or other-readable. Recursive downloads and downloads of two or more sources arrive as an archive whose entries carry no Unix mode, so each extracted file lands at `0666` before the umask whatever its mode was on the server, and each directory created along the way at `0777` before the umask. A local file the archive overwrites keeps its own mode there too, and no warning covers that path. Saving in `edit` overwrites the remote file; ownership and permissions may be reset by server policy. `edit` only opens existing remote files—it downloads first, so it won't create a new one. `--editor` is tokenized without a shell (the file path is appended as the last argument), so shell syntax such as pipes (`|`), redirections (`>>`), or `&&` won't work.

### TCP tunneling
```bash
$ alpacon tunnel <server> -l 9000 -r 8082
$ alpacon tunnel prod-db -l 5432 -r 5432 -- psql -h 127.0.0.1 -p 5432 -U app appdb
$ alpacon tunnel prod-k8s -l 6443 -r 6443 -- kubectl --server=https://127.0.0.1:6443 get pods
```

`--` separates the tunnel command from the inner command. `alpacon tunnel` does not auto-detect app ports—pass `127.0.0.1:<LOCAL_PORT>` explicitly.

### Work sessions
```bash
$ alpacon work-session ls                          # my active sessions (default)
$ alpacon work-session ls --status all             # my sessions in any status
$ alpacon work-session ls --user all               # everyone's active sessions
$ alpacon work-session ls --user all --status all  # all sessions
$ alpacon work-session current
$ alpacon work-session use <session-id>          # set active session
$ alpacon work-session use --unset
$ alpacon work-session revoke <session-id>       # superuser
$ alpacon work-session cancel <session-id>       # requester withdraws own pending request
# Approving/rejecting a session happens in the Alpacon console (web), not the CLI.
```

Override the active session per command with `--work-session <id>` or `ALPACON_WORK_SESSION=<id>`. Resolution order: `--work-session` flag > env var > active session.

### Identity (users, groups)
```bash
$ alpacon user ls
$ alpacon user describe <username>
$ alpacon user create / update / rm
$ alpacon group ls
$ alpacon group member add --group <group> -u <user> --role <role>
$ alpacon group member rm --group <group> -u <user>
```

### Workspace roles
RBAC roles are the single source of truth for what an account is and may do. Granting `admin` is what makes someone a workspace administrator, and granting `superuser` is what makes someone a platform operator—the `is_staff` and `is_superuser` fields on a user are read-only projections of those roles, so editing them in `alpacon user update` does nothing and the command says so.

```bash
$ alpacon user role ls <username>                   # roles a user holds, and at what scope
$ alpacon user role catalog                         # roles this workspace defines
$ alpacon user role describe <role>                 # what it grants, and who holds it
$ alpacon user role grant  <username> <role> --reason "<why>"
$ alpacon user role revoke <username> superuser --cascade
$ alpacon user role history <username>              # who changed what, and why
$ alpacon user permission ls <username>             # what those roles let them do
$ alpacon user permission can-i <username> server:update
```

Granting `superuser` also creates a companion `admin` binding, so revoking `superuser` demotes to admin and leaves that companion in place; `--cascade` removes both. Changing a binding requires a workspace superuser and recent MFA.

On Alpacon Cloud workspaces an API token is refused on the `alpacon user role` commands, reads included, so they need a browser login. Two carve-outs: `alpacon user role history` reads the audit log, which accepts a token carrying the `role_audit_log:read` scope on either deployment; and `alpacon user permission` is hosted on the IAM user endpoint, which accepts a token on either deployment—except `can-i --explain`, which posts to the RBAC troubleshooter. On self-hosted workspaces a token works throughout and skips the MFA step.

### API tokens
```bash
$ alpacon token create -n <name> --expiration-in-days=7
$ alpacon token ls
$ alpacon token rm <token-id-or-name>
$ alpacon login <URL> -t <TOKEN_KEY>
```

### Token ACLs
Each API token gets three independent **deny-by-default** ACL types—`command` (which shell commands the token can run via websh/exec), `server` (which servers it can reach), and `file` (which file paths it can read/write via cp). A bare token can do nothing until at least one ACL of each relevant type is granted; this is how `damage containment` is enforced on the token-auth path (`work session` plays the same role on the interactive-auth path).

```bash
$ alpacon token acl command add my-token --command="docker *" --username=root
$ alpacon token acl server  add my-token --servers web-01,web-02
$ alpacon token acl file    add my-token --path "/home/deploy/*" --action upload
$ alpacon token acl <type> ls     my-token
$ alpacon token acl <type> delete <acl-id>
```

### Agent (Alpamon) management
```bash
$ alpacon agent restart  <server>
$ alpacon agent upgrade  <server>
$ alpacon agent shutdown <server>
```

### Logs and audit
```bash
$ alpacon log <server> --tail=10
$ alpacon audit <filters>                        # workspace audit log
```

### More commands

Run `alpacon --help` for the full list, or `alpacon <command> --help` for details on any command.

## When a command is denied

Under interactive auth (browser login), `websh`, `exec`, `cp`, `edit`, and `tunnel` require an active work session. Without one, the command is refused with a diagnostic and exit code `3`:

```
Error: the command operation requires an active WorkSession on this authentication.

  auth          : Browser login (interactive)
  reason        : no WorkSession selected for this shell
  required scope: command
  target server : prod-1

Next:
  alpacon work-session ls --status active  # find an existing active session; AI agent: reuse it by prefixing the gated command with --work-session <ID>
  alpacon work-session use <ID>  # human: attach an existing session (rejects agent sessions)
  alpacon work-session create --scope command --server prod-1 --expires-in 1h --purpose "<intent>" --use  # none active? create a new one (human)
  alpacon work-session create --scope command --server prod-1 --expires-in 1h --purpose "<intent>" --requester-type agent  # none active? create a new one (AI agent; prefix the gated command with --work-session <ID>)

Note: Tokens issued by Alpacon (service or personal API token) bypass this check.
```

With `--output json`, the same refusal is a structured envelope on stderr—scripts and AI agents branch on `error_code` and exec each `next_actions[].command` directly (the human hint, when present, is a separate `description` field):

```json
{
  "ok": false,
  "exit_code": 3,
  "error_code": "work_session_required",
  "message": "the command operation requires an active WorkSession on this authentication.",
  "reason": "no WorkSession selected for this shell",
  "context": {
    "auth_method": "Browser login",
    "required_scope": "command",
    "target_servers": ["prod-1"],
    "current_worksession": null
  },
  "next_actions": [
    {"command": "alpacon work-session ls --status active", "description": "find an existing active session; AI agent: reuse it by prefixing the gated command with --work-session <ID>"},
    {"command": "alpacon work-session use <ID>", "description": "human: attach an existing session (rejects agent sessions)"},
    {"command": "alpacon work-session create --scope command --server prod-1 --expires-in 1h --purpose \"<intent>\" --use", "description": "none active? create a new one (human)"},
    {"command": "alpacon work-session create --scope command --server prod-1 --expires-in 1h --purpose \"<intent>\" --requester-type agent", "description": "none active? create a new one (AI agent; prefix the gated command with --work-session <ID>)"}
  ]
}
```

What each refusal code means and what to do next:

| `error_code` | Meaning | Next |
|---|---|---|
| `work_session_required` | no session selected for this shell | `work-session create --use` or `work-session use <ID>` |
| `work_session_not_active` | session not active (pending, approved, completed, revoked, or cancelled) | if pending or approved, wait; otherwise create or reuse a session |
| `work_session_expired` | session has expired | `work-session extend <ID>` or create a new one |
| `work_session_scope_not_allowed` | operation not in session scopes | create a session with the right `--scope` |
| `work_session_server_not_allowed` | target server not in session | create a session with the right `--server` |
| `work_session_assignee_mismatch` | session assigned to another principal | `work-session use <ID>` with your own session |
| `work_session_not_usable` | session is no longer usable | `work-session create --use` |

`work-session` subcommand failures (`create`, `use`, `extend`, ...), `event wait` / `event watch` failures, and the inline-credential refusal below also emit a JSON error envelope under `--output json`, with `error_code` carrying the server code when available, and `exit_code` matching the process exit code—`1` for a failed request, `2` with `error_code` `usage_error` for a flag or argument the command rejected. These envelopes may share an `error_code` with the gate-denial envelopes above—distinguish a subcommand failure (`exit_code: 1` or `2`) from a gate denial (`exit_code: 3`) via `exit_code`, not `error_code` alone. Run `alpacon whoami` to check upfront whether a work session is required for your auth.

Separately from the work session gate, `exec` (and `websh` when running a command) is refused before the command runs if the command line itself carries a credential—a `-p`/`--password` flag, a `KEY=VALUE` secret such as `PGPASSWORD=...`, or a `user:pass@host` connection string. Pass the secret with `--env="KEY"` instead: its value is read from your shell, so it never lands on the command line the server stores. The refusal is permanent—a retry submits the same command line—so rewrite rather than retry.

Exit code is `1`, and under `--output json` the refusal is an error envelope on stderr with `error_code` `command_inline_credential`. That envelope currently carries no `next_actions`—rewrite the command with `--env="KEY"` as described above (`exec` takes the remote command after `--`, `websh` takes it as one quoted argument).

Also separate from the work session gate: when you authenticate with an API token or a service token, the server checks the request against that token's ACL rules before anything runs. A request outside those rules is refused with exit code `1` and a message naming token access control. This is a permanent refusal, not a transient one: a retry submits the same request. List the rules with `alpacon token acl command ls TOKEN`, `alpacon token acl server ls TOKEN`, or `alpacon token acl file ls TOKEN`, and widen them with the matching `add` subcommand. Deny-by-default applies per ACL type—a token with no rule of a given type has no access of that kind at all.

## Exit codes

| Code | Meaning |
|------|---------|
| `0`  | Success |
| `1`  | General error (network failure, server error, etc.), and permanent refusals such as a credential on the command line (`command_inline_credential`)—see "When a command is denied". Also: `alpacon user update` when the edit touched `is_staff` or `is_superuser`, which are read-only and are never sent (any other field in the same edit is still applied); and a negative answer from `alpacon user permission can-i -q`, which is indistinguishable from a failed check by exit code alone—drop `-q` and read the `allowed` field of `--output json` when a script has to tell them apart |
| `2`  | Usage error—a flag or argument rejected by a `work-session` subcommand, by `event wait` / `event watch`, or by the positive-value check behind `--tail`, `--limit`, and `--valid-days` (`utils.ExitCodeUsageError`). Under `--output json` the `work-session` and `event` paths emit an error envelope carrying `error_code` `usage_error`; the positive-value check prints a plain message to stderr. Every other command still exits `1` when it rejects a flag or argument, as do errors Cobra itself rejects while parsing (an unknown flag or subcommand) |
| `3`  | WorkSession gate denied—the active session does not authorize this action |
| `4`  | Pending human approval—the action is awaiting an out-of-band approve/reject in the Alpacon console (web/Slack), not refused. For `exec`, re-run the command after approval (or pass `--wait` on the original command to block; `--wait-approval <duration>` raises the wait timeout, default 5m); `websh` command mode has no `--wait`, so re-run via `alpacon exec --wait` to block; for `work-session create` the session already exists—after approval attach it with `alpacon work-session use <id>` (or pass `--wait` on the original create to block; `--wait-approval <duration>` raises the wait timeout, default 5m). Under `--output json`, a `{"status":"pending_approval", ...}` object is emitted. Returned by `exec` (and `websh` when running a command) on a sudo denial that created an approval request (`SUDO_APPROVAL_REQUIRED`, or `SUDO_INTENT_DEVIATION` when the command reads as off-purpose for the work session)—including when `exec --wait` ends with the request still open, whether the window elapsed or the CLI could not reach the server for a bounded run of polls—and by `work-session create` when the session lands pending or when its `--wait` ends with the outcome still open, whether the window elapsed or the CLI could not reach the server for a bounded run of polls. Also returned by `alpacon event wait` when the wait times out or is interrupted, and by `alpacon exec logs` on a job still held for approval—the outcome is still open in both |
| `5`  | Server busy with active user work—a disruptive `server` action (`reboot`, `shutdown`, `upgrade`) was refused because the server has an open Websh/WebFTP session or in-flight command. Transient and retryable: retry when idle, or re-run with `--force` to override |
| `6`  | Approval not granted—an awaited approval settled without being granted (rejected, expired, revoked, cancelled, or completed). Distinct from `4`: the outcome is final, so retrying the same request only generates another approval request. Returned by `alpacon event wait`, by `work-session create --wait`, and—when a reviewer rejected the command itself—by `exec`, by `websh` command mode, and by `alpacon exec logs`. Under `--output json` these paths emit an error envelope carrying the same `exit_code` |
| `7`  | Purpose required—the verification gate held an agent's command and is asking what it is for, before any approval request exists (ADR 0052). Distinct from `4`: nothing is pending on a human, nobody has been notified, and the next move belongs to the caller that submitted the command. Answer with `alpacon exec purpose <JOB_ID> "..."` within about a minute; `--wait` does not apply, because the answer is yours to give rather than somebody else's to grant. Pass `--purpose` on the original `exec` to state it up front and skip the demand entirely. One demand per command: on silence the command takes the ordinary path, and a late or second answer is refused. Under `--output json`, a `{"status":"purpose_required", ...}` object is emitted carrying the command id, the seconds left (when the server reports the demand's expiry), and what makes a purpose useful. `alpacon exec purpose` answers on the same contract, emitting `purpose_recorded` on success and `purpose_refused` (exit `1`) when the demand is already settled or the credential is not the one that submitted the command. Returned by `exec`, by `websh` command mode (which reaches the same handler through `RunRemoteExec` but has no `--purpose`, so a demand there can only be answered with `alpacon exec purpose`), and by `alpacon exec logs` — the only sight `--detach` has of a demand, and worth knowing because a held command sits for the length of the window before taking the ordinary path |

## Contributing

```bash
git clone https://github.com/alpacax/alpacon-cli.git
cd alpacon-cli
go build
go test ./...
```

### End-to-end tests against a live workspace

`sample_test_cli.sh` exercises the major commands (server lookup, exec, websh, cp, tunnel) against a real Alpacon workspace. Copy it, fill in the workspace URL and target server at the top, and run:

```bash
cp sample_test_cli.sh test_cli.sh
$EDITOR test_cli.sh                              # set WORKSPACE_URL, SERVER_NAME
chmod +x test_cli.sh && ./test_cli.sh
```

Bug reports and feature requests welcome at [GitHub Issues](https://github.com/alpacax/alpacon-cli/issues).

## License

[MIT License](LICENSE). Copyright © 2026 AlpacaX Inc.
