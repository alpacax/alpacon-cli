---
name: alpacon
description: >-
  Operate remote servers through the Alpacon CLI (alpacon, alias ac)—login,
  work sessions, remote command execution, file transfer, TCP tunnels, and
  structured JSON error handling. Use when running alpacon commands, accessing
  servers managed by Alpacon, or automating server operations in scripts and CI.
metadata:
  cli-version: unknown
---

# Alpacon CLI for AI agents

Alpacon is an AI-native PAM: every command you run on a managed server is
judged at runtime, recorded, and bounded by a scoped work session. This skill
covers what `--help` does not—the work-session gate, exit codes, and the JSON
error contract.

## Preflight

Always start by checking who you are and whether a work session is required:

```bash
alpacon whoami --output json
```

- Not logged in? Run `alpacon login`—it opens a browser for OAuth, so a human
  must complete it. In headless environments set `ALPACON_NO_BROWSER=1` and
  follow the printed URL instructions.
- CI and automation use token auth, which bypasses work sessions but is
  bounded by deny-by-default token ACLs:

```bash
alpacon login <URL> -t <TOKEN_KEY>
```

## Output contract

Always pass `--output json`. Data goes to stdout; errors and refusals are JSON
envelopes on stderr. Branch on `exit_code` first, then `error_code`:

| Exit code | Meaning | What to do |
|---|---|---|
| `0` | success | — |
| `1` | general error | inspect `message`; retry only if transient |
| `2` | usage error | fix flags or arguments |
| `3` | work-session gate denied | run the envelope's `next_actions` verbatim |
| `4` | pending human approval | not refused—wait for out-of-band approval, then re-run; or pass `--wait` upfront to block |

Never parse table output; always use `--output json`.

## Work sessions

Under interactive (browser) auth, `websh`, `exec`, `cp`, `edit`, and `tunnel`
require an active work session. A refusal (exit code `3`) emits a JSON
envelope on stderr whose `next_actions` are executable commands—run them
verbatim. Typical agent flow:

```bash
# 1. Look for an existing active session.
alpacon work-session ls --status active --output json

# 2. None? Create one scoped to the task. Agent sessions cannot self-approve;
#    a human approves out-of-band (exit code 4 while pending).
alpacon work-session create \
  --purpose "<what you are doing and why>" \
  --scope command \
  --server <server> \
  --expires-in 1h \
  --requester-type agent \
  --output json

# 3. Run gated commands with the session ID.
alpacon exec --work-session <ID> <server> "uptime"
```

- Always pass `--requester-type agent` when you drive the session. Humans
  attach sessions with `alpacon work-session use <ID>`, but that rejects
  agent sessions—reference yours explicitly with `--work-session <ID>` or the
  `ALPACON_WORK_SESSION` env var. Resolution order: flag > env var > active
  session.
- Non-interactive sudo via `exec` requires pre-declared `--sudo` patterns on
  the session; without them a sudo denial returns exit code `4`.

Gate refusal codes (exit code `3`):

| `error_code` | Meaning | Next |
|---|---|---|
| `work_session_required` | no session selected for this shell | create or reuse a session |
| `work_session_not_active` | session pending, approved, completed, or revoked | if pending or approved, wait; otherwise create or reuse |
| `work_session_expired` | session has expired | `alpacon work-session extend <ID>` or create a new one |
| `work_session_scope_not_allowed` | operation not in session scopes | create a session with the right `--scope` |
| `work_session_server_not_allowed` | target server not in session | create a session with the right `--server` |
| `work_session_assignee_mismatch` | session assigned to another principal | use a session of your own |
| `work_session_not_usable` | session no longer usable | create a new session |

`work-session` subcommand failures reuse these `error_code` values with
`exit_code: 1`—always branch on `exit_code`, not `error_code` alone.

## Command syntax

`websh`, `exec`, and `cp` accept SSH-like `[USER@]SERVER` targets. For `exec`,
flags go before the server name; use `--` to pass flags to the remote command.

```bash
alpacon exec <server> "uptime"
alpacon exec root@<server> "docker ps"
alpacon cp ./local.txt <server>:/tmp/
alpacon cp <server>:/var/log/app.log .
alpacon tunnel <server> -l 9000 -r 8082
```

Resources follow `<resource> <verb>`—for example:

```bash
alpacon server ls
alpacon server describe <server>
```

Aliases: `ls` for list, `rm` for delete, `desc` for describe.

## Cautions

- `alpacon websh <server>` opens an interactive TTY—unsuitable for agents.
  Use `alpacon exec` instead.
- Delete commands (`server rm`, `token rm`, ...) are irreversible. Confirm
  intent before running them.
- MFA prompts may open a browser; `ALPACON_NO_BROWSER=1` suppresses this and
  prints the URL instead.
