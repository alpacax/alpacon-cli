# Work-session switching + flag override design

**Date**: 2026-05-11
**Issue**: [alpacax/alpacon-cli#162](https://github.com/alpacax/alpacon-cli/issues/162)
**Branch**: `worksession-switching`
**Status**: Draft — pending user review

## 1. Background

Work sessions group Alpacon operations under an approval-gated, time-bounded, named unit for audit trail and AI analysis. Today, `exec`, `websh`, `cp`, and `tunnel` run outside any session context. Issue #162 proposes a `--work-session <UUID>` flag (and `ALPACON_WORK_SESSION` env var) so every CLI operation can be attributed to a session.

This document extends the issue with a persistent "active session" mechanism (`work-session use <UUID>`) following the `workspace switch` precedent, so users don't have to repeat the flag on every command. The flag retains override priority.

### Divergences from issue #162

| Issue #162 | This design | Rationale |
|---|---|---|
| `ALPACON_WORK_SESSION` env var fallback | **Omitted** | `use` covers the persistent case; env var would add a third tier with marginal value. AI agents/CI scripts can call `use` once at startup. |
| Resolution: `flag > env` | `flag > config (use)` | New persistent tier replaces env var. |
| No client-side validation | `use` validates once at set time | UX: catch typos/permission errors at the explicit set action. Per-call validation still server-side only. |

## 2. User-facing surface

### 2.1 New commands

```
alpacon work-session use <UUID>
alpacon work-session use --unset
alpacon work-session current
```

- `use <UUID>` — sets the active work-session for the current workspace. Server-validated (`GET /api/work-sessions/<UUID>/`) before save. Rejected if not found, not accessible, or not in `ACTIVE` state.
- `use --unset` — clears the active work-session for the current workspace. Idempotent (no error if already empty).
- `current` — prints the active work-session for the current workspace. Honors `--output table|json`. Exit 0 in all "no active session" cases.

### 2.2 Extended commands

```
alpacon work-session ls               # ' * ' marker prefix on the active row
alpacon exec     --work-session <UUID> ...
alpacon websh    --work-session <UUID> ...
alpacon cp       --work-session <UUID> ...
alpacon tunnel   --work-session <UUID> ...
```

`ls` marker only shows in table output. JSON output is unchanged.

### 2.3 Resolution order (exec / websh / cp / tunnel)

```
1. --work-session <UUID>                             ← one-off override, wins
2. config.ActiveWorkSessions[current workspace]      ← set via `use`
3. (none) → no work_session field in request body    ← current behavior
```

No one-time bypass flag (e.g., `--no-work-session`). To run without a session when one is set, call `work-session use --unset` first.

### 2.4 Execution notice

Before the API call in `exec/websh/cp/tunnel`, when a resolved UUID is non-empty, print to stderr:

```
Using work-session ses-abc123
```

ID-only — avoids extra API roundtrip for the name. Stderr keeps stdout clean for `--output json` consumers and pipelines.

## 3. Architecture

### 3.1 Server contract (alpacon-server)

`work_session` is a **request body field** (DRF `PrimaryKeyRelatedField`, `required=False, allow_null=True`) on the create endpoints for:

| CLI command | Server module | Status |
|---|---|---|
| `exec` | `events` (Command) | Ready — `events/api/serializers.py` has `work_session` |
| `websh` | `websh` | Ready — `websh/api/serializers.py` has `work_session` |
| `tunnel` | `websh` (TunnelSession) | Ready — same file |
| `cp` | `ftp` | **Not ready** — server-side PR required |

The CLI sends `"work_session": "<UUID>"` in the JSON body. Server enforces scope (operation type matches session scopes), status (session must be ACTIVE), assignee (caller must be the requester), and server membership (target server must be in the session's `servers`).

**Cross-repo dependency**: `cp` cannot benefit from `--work-session` until the `ftp` serializer is updated server-side. The CLI implementation still adds the field (no-op until server lands).

### 3.2 CLI architecture

```
cmd/worksession/
  worksession.go              (existing) — root command
  worksession_use.go          (new)      — `use <UUID> [--unset]`
  worksession_current.go      (new)      — `current`
  worksession_list.go         (modified) — adds ' * ' marker
  resolve.go                  (new)      — Resolve(flag) → UUID

config/
  types.go                    (modified) — adds ActiveWorkSessions map field
  config.go                   (modified) — Set/GetActiveWorkSession helpers

api/event/event.go            (modified) — RunCommand gets workSessionID param
api/websh/websh.go            (modified) — CreateWebshSession gets workSessionID
api/ftp/ftp.go                (modified) — Upload/DownloadFile get workSessionID
api/tunnel/tunnel.go          (modified) — CreateTunnelSession gets workSessionID

cmd/exec/exec.go, parse.go    (modified) — flag parsing + RunCommandWithRetry
cmd/exec/run.go               (modified) — RunCommandWithRetry signature
cmd/websh/websh.go            (modified) — manual flag parsing for --work-session
cmd/ftp/cp.go                 (modified) — flag + API call wiring
cmd/tunnel/run.go             (modified) — flag + API call wiring
```

### 3.3 Config schema

```go
// config/types.go
type Config struct {
    WorkspaceURL         string            `json:"workspace_url"`
    WorkspaceName        string            `json:"workspace_name"`
    // ... existing fields ...
    ActiveWorkSessions   map[string]string `json:"active_work_sessions,omitempty"`
}
```

- Key: `WorkspaceName` (matches the current workspace identifier)
- Value: work-session UUID string
- `omitempty` keeps existing user configs intact (nil-safe load)
- Setting `""` removes the key (not store empty string) for lookup consistency

### 3.4 Resolver

```go
// cmd/worksession/resolve.go

func Resolve(flagValue string) (string, error) {
    if flagValue != "" {
        return flagValue, nil
    }
    return config.GetActiveWorkSession()
}

// Prints "Using work-session <uuid>" to stderr when uuid != ""
func AnnounceIfActive(uuid string)
```

### 3.5 API signature changes

```go
func RunCommand(ac *client.AlpaconClient, serverName, command, username, groupname string,
    env map[string]string, workSessionID string) (string, error)

func CreateWebshSession(ac *client.AlpaconClient, serverName, username, groupname string,
    share, readOnly bool, workSessionID string) (SessionResponse, error)

func UploadFile(ac *client.AlpaconClient, src []string, dest, username, groupname string,
    allowOverwrite bool, workSessionID string) error

func DownloadFile(ac *client.AlpaconClient, sources []string, dest, username, groupname string,
    recursive bool, workSessionID string) error

func CreateTunnelSession(ac *client.AlpaconClient, serverName, username, groupname string,
    targetPort int, workSessionID string) (*TunnelSessionResponse, error)
```

Body serialization rule (each API function):

```go
if workSessionID != "" {
    body["work_session"] = workSessionID
}
```

`exec.RunCommandWithRetry` (shared by `exec` and `websh` command-mode) also gains the `workSessionID` parameter.

## 4. Error handling and edge cases

### 4.1 `work-session use <UUID>`

| Condition | Result |
|---|---|
| Not logged in | `"Not logged in. Run 'alpacon login' first."` exit 1 |
| Server 404 | `"Work session not found: <uuid>"` exit 1 |
| Server 403 | `"You do not have access to work session <uuid>"` exit 1 |
| Server returns session with status ≠ ACTIVE | `"Work session <uuid> is in '<status>' state and cannot be used."` exit 1 |
| Other server error | original error message, exit 1 |
| Happy path | save to config, `"Active work-session set to <uuid> (<name>)."` exit 0 |

### 4.2 `work-session use --unset`

| Condition | Result |
|---|---|
| No active session set | `"No active work-session to unset."` exit 0 (idempotent) |
| Active session set | clear, `"Active work-session cleared."` exit 0 |
| `--unset` + positional UUID | argument validation error |

### 4.3 `work-session current`

| Condition | Result (`--output table`) | Result (`--output json`) |
|---|---|---|
| No active session | `"No active work-session."` to stderr, exit 0 | `null` to stdout, exit 0 |
| Active session valid | Same single-row table as `ls` for that session | Detail JSON |
| Active UUID no longer exists server-side | `"Active work-session <uuid> no longer exists. Run 'alpacon work-session use --unset' to clear."` exit 1 | same to stderr, exit 1 |

### 4.4 `exec/websh/cp/tunnel` resolution outcomes

- Neither flag nor config → request body has no `work_session` field, current behavior preserved
- Flag set → use flag UUID, ignore config, print notice
- Config set, no flag → use config UUID, print notice
- Server rejects (scope/status/assignee mismatch) → server error surfaced verbatim, no extra client-side checks

### 4.5 Workspace switch interaction

`workspace switch <name>` does nothing special with active work-sessions. Per-workspace storage means each workspace has its own independent entry. Switching back restores the previous active session.

### 4.6 Backward compatibility

- Existing `config.json` without `active_work_sessions` key — JSON unmarshals to nil map. `GetActiveWorkSession` returns `""` without panic.
- Existing users not using `--work-session` — zero behavioral change.

## 5. Testing

### 5.1 Config layer (`config/config_test.go`)

- Set → Get round-trip for current workspace
- Set `""` removes key from map
- Per-workspace isolation: A stored, switch to B, Get returns "", switch back to A, Get returns A's UUID
- Load legacy config without the key → no panic, Get returns ""

### 5.2 Resolver (`cmd/worksession/resolve_test.go`)

Table-driven:

| flagValue | config value | expected |
|---|---|---|
| `""` | `""` | `""` |
| `""` | `"uuid-cfg"` | `"uuid-cfg"` |
| `"uuid-flag"` | `""` | `"uuid-flag"` |
| `"uuid-flag"` | `"uuid-cfg"` | `"uuid-flag"` |

`AnnounceIfActive` — capture stderr and assert presence/absence based on UUID.

### 5.3 API layer (`api/event/`, `api/websh/`, `api/ftp/`, `api/tunnel/` test files)

For each modified function:

- `httptest.NewServer` handler asserts request body JSON
- `workSessionID == ""` → body has no `work_session` key
- `workSessionID == "uuid-x"` → body has `"work_session": "uuid-x"`

### 5.4 New command tests (`cmd/worksession/worksession_use_test.go`)

- `use` happy path (mock 200 + ACTIVE)
- `use` failure paths (404, 403, non-ACTIVE status)
- `use --unset` idempotency
- `current` (active / none / stale-UUID branches)

### 5.5 List marker (`cmd/worksession/worksession_list_test.go`)

- With active session matching a row → ` * ` prefix
- JSON output → no marker

### 5.6 Flag parsing regression (`cmd/exec/parse_test.go`, `cmd/websh/websh_test.go`)

- `exec --work-session uuid server cmd`
- `exec --work-session=uuid server cmd`
- `exec server -- ls --work-session` (post-`--` belongs to remote command)
- `websh --work-session uuid -u root server` (multiple flags coexist)

### 5.7 Out of scope

No integration tests against a live alpacon-server. Matches existing CI policy.

## 6. Implementation phases

### Phase 1 — Config + Resolver foundation
- `config/types.go`: add `ActiveWorkSessions` field
- `config/config.go`: `SetActiveWorkSession`, `GetActiveWorkSession` helpers
- `config/config_test.go`: per-workspace isolation tests
- `cmd/worksession/resolve.go`: `Resolve`, `AnnounceIfActive`
- `cmd/worksession/resolve_test.go`: priority table

### Phase 2 — `work-session use` and `current` commands
- `cmd/worksession/worksession_use.go`: `use <UUID> [--unset]` with server validation via existing `GetWorkSession`
- `cmd/worksession/worksession_current.go`: `current` with table/JSON output
- Register in `worksession.go` `init()`
- Tests for both commands

### Phase 3 — `work-session ls` marker
- `cmd/worksession/worksession_list.go`: prepend ` * ` to active row in table output
- Test update

### Phase 4 — API signature changes
- `api/event/event.go`: add `workSessionID` param to `RunCommand`, conditional body field
- `api/websh/websh.go`: same for `CreateWebshSession`
- `api/ftp/ftp.go`: same for `UploadFile`/`DownloadFile`
- `api/tunnel/tunnel.go`: same for `CreateTunnelSession`
- API-layer tests per file

### Phase 5 — Command wiring
- `cmd/exec/run.go`: `RunCommandWithRetry` signature update
- `cmd/exec/exec.go` + `parse.go`: parse `--work-session`, resolve, pass through
- `cmd/websh/websh.go`: manual parser handles `--work-session`, command-mode passes to `RunCommandWithRetry`, session-mode passes to `CreateWebshSession`
- `cmd/ftp/cp.go`: standard cobra flag, pass to upload/download
- `cmd/tunnel/run.go`: standard cobra flag, pass to `CreateTunnelSession`
- Flag parsing regression tests for exec/websh

### Phase 6 — Documentation
- Update each command's `Long` / `Example` / `Flags` block in cobra definitions
- Update `cmd/worksession/worksession.go` `Long` to mention `use` / `current`

## 7. Cross-repo coordination

| Repo | Action | Blocking? |
|---|---|---|
| `alpacon-server` (`ftp` serializer) | Add `work_session` field | Yes for `cp`, no for others |
| `alpacon-server` (events/websh/tunnel) | Already shipped | n/a |
| `alpamon` | No change | n/a |
| `alpacon-protos` | No change (REST-only path) | n/a |

`cp --work-session` should not be advertised in release notes until the server `ftp` PR ships. The CLI code can ship and is a no-op for `cp` until then.

## 8. Out of scope

- `editor` and `sudo` CLI commands (no current CLI surface; same pattern when added)
- Environment variable `ALPACON_WORK_SESSION` (explicitly excluded per design decision)
- One-time bypass flag like `--no-work-session` (excluded — use `use --unset`)
- Auto-clearing stale active sessions (server-side rejection surfaces; user decides)
- Showing session name in execution notice (omitted to avoid extra API call)
