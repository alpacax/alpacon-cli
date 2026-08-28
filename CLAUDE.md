# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project overview

Alpacon CLI (`alpacon`, alias `ac`) is the command-line client for [Alpacon](https://alpacon.io), the AI-native PAM. With Alpacon, humans, AI agents, and CI/CD pipelines reach and operate every server in your fleet through a single identity—and every command they run is judged at runtime, recorded, and bounded by a scoped work session. Built with Go and [Cobra](https://github.com/spf13/cobra).

If a credential leaks or an AI client is compromised, the damage is bounded by the session, not by what the credential could touch. The CLI is the terminal-side surface for that model and is used by engineers, AI coding agents (Claude Code, GitHub Copilot, Cursor, Codex CLI, Gemini CLI), and CI/CD platforms.

## Development commands

### Build

```bash
go build -o alpacon .
```

### Test

```bash
# With race detection (same as CI)
go test -race -v ./...
```

### Lint

```bash
# CI uses golangci-lint (see .golangci.yml for enabled linters)
golangci-lint run ./...

# Quick check
go vet ./...
```

## Architecture

### Project structure

```
main.go              # Entry point
cmd/                 # Cobra command definitions
  root.go            # Root command, registers all subcommands
  login.go           # Login command
  logout.go          # Logout command
  version.go         # Version command
  agent/             # alpacon agent
  authority/         # alpacon authority
  cert/              # alpacon cert
  csr/               # alpacon csr
  event/             # alpacon event
  exec/              # alpacon exec
  ftp/               # alpacon cp (file transfer)
  iam/               # alpacon user, alpacon group
  log/               # alpacon log
  note/              # alpacon note
  packages/          # alpacon package
  server/            # alpacon server
  token/             # alpacon token
  tunnel/            # alpacon tunnel
  websh/             # alpacon websh
  workspace/         # alpacon workspace
api/                 # API client functions per domain
client/              # HTTP client wrapper for Alpacon API
config/              # Configuration management (credentials, workspace)
pkg/                 # Internal packages (cert, tunnel)
utils/               # Shared utilities (output, prompts, errors, SSH parsing)
```

### Key patterns

- **Command registration**: All commands are registered in `cmd/root.go` via `RootCmd.AddCommand()`
- **API layer**: Each `cmd/` package calls corresponding `api/` package for HTTP requests
- **SSH-like syntax**: `websh`, `exec`, `cp` support `user@host` syntax via `utils.ParseSSHTarget()`
- **Error handling**: Common errors (MFA required, username required) are handled via `utils.HandleCommonErrors()` with retry callbacks. `mfa.ErrorCallbacks(ac, retry)` builds the standard callback set for a command that already holds a client—use it instead of writing the whole set out again. Commands whose callbacks genuinely differ construct `utils.ErrorHandlerCallbacks` themselves: `tunnel` creates its client lazily and takes the legacy retry path; `server` needs no username handling; `workspace` needs none either and opens a workspace-scoped MFA link, which its two update commands share through `errorCallbacks` in `cmd/workspace`. `utils` cannot host the helper—`api/mfa` and `api/iam` both import `utils`, so `utils` importing either back would be an import cycle
- **Custom flag parsing**: `websh` and `exec` use `DisableFlagParsing: true` and parse flags manually. `exec` supports `--` separator for remote command flags
- **Shared command execution**: `exec.RunCommandWithRetry()` wraps `event.RunCommand()` + `HandleCommonErrors()` with MFA/retry logic. Used by both `exec` and `websh`
- **Browser auto-open**: `utils.OpenBrowser()` opens auth URLs with SSH/headless detection, cross-process debounce (`~/.alpacon/.browser_lock`), and `ALPACON_NO_BROWSER` env var opt-out
- **Output format flag**: `--output` persistent flag on `RootCmd` (`table` | `json`, default `table`), bound to `utils.OutputFormat` global in `cmd/root.go`. No short form—`-o` is reserved for subcommand-local `--out` flags (e.g., `cert download -o path`). `--output json` produces pretty-printed JSON (2-space indent) on stdout via `utils.PrintTable()` / `utils.PrintJson()`; default preserves existing behavior (table for list commands, pretty JSON for detail commands). Empty/nil slices emit `[]`
- **Table output**: API response → `*Attributes` struct projection → `utils.PrintTable()`. All list commands follow this pattern
- **Pagination**: `api.FetchAllPages[T]` (generics) handles all pagination internally. `cmd/` layer never sees pagination. For a list bounded by `--tail`, use `api.FetchPagesUpTo[T]` (PageNumber) or `api.FetchCursorPages[T]` (Elasticsearch cursor) so only the requested pages are fetched—the server caps `page_size` at 100, so a single request cannot satisfy a larger `--tail`
- **`--tail` semantics**: `--tail N` means the newest N entries, never the physical end of a descending list. Endpoints that sort newest first satisfy it from the first page; a helper that slices `list[len-N:]` would return the oldest N instead
- **Dual auth tokens**: `AccessToken` (Auth0 Bearer JWT) takes priority; `Token` (legacy API key) is fallback. Set in `client.setHTTPHeader()`
- **Stale-token renewal**: `NewAlpaconAPIClient()` refreshes an expired access token once, at construction—which never fires again for a client held through a long wait. `client.sendRequest()` therefore renews mid-flight: a 401 whose body is a JSON object carrying no error code is the only 401 a fresh token could plausibly move, so it runs the refresh-token grant and replays the request once. The server's Auth0 authenticator returns no user on every bearer rejection, whether it absorbs an exception or declines outright, collapsing an expired token and every other one alike into DRF's uncoded `NotAuthenticated`—so the empty code slot is not proof of expiry, only the absence of a stated refusal. A coded 401 names what it wants (MFA required, IP not allowed, token ACL) and a new token is not it, so it is never renewed on. The JSON-object test is what keeps a gateway out of the grant—a proxy, a WAF or an mTLS gate answers 401 with an HTML page or nothing, and no token this process can obtain moves that answer. Only a bearer request qualifies—a legacy API key or a service token has no refresh token behind it—and only a body `net/http` can rewind is replayed. Concurrent requests sharing one expiry spend one grant: it runs under `refreshMu`, never under the `tokenMu` a request takes to read the token—the grant is unbounded network I/O, and holding that lock across it would stall every request the client has in flight
- **SaaS vs self-hosted detection**: Use `config.IsSaaS()` (package-level function in `config/config.go`) to detect deployment type. Returns `true` when `AccessToken` is present (Auth0 login = Alpacon Cloud). Add early-exit guards before `NewAlpaconAPIClient()` in commands that are SaaS-only or self-hosted-only:
  ```go
  isSaaS, err := config.IsSaaS()
  if err != nil {
      utils.CliErrorWithExit("Not logged in. Run 'alpacon login' first.")
  }
  if !isSaaS {
      utils.CliErrorWithExit("This command is only available on Alpacon Cloud workspaces.")
  }
  ```
- **CLI output helpers**: `utils.CliError*/CliInfo*/CliWarning` all write to stderr. stdout is reserved for data output (tables, JSON)
- **Group commands**: `RunE`—returns error to trigger help when no subcommand is given
- **Leaf commands**: `Run` + `utils.CliErrorWithExit`—preserves colored output; `RunE` would print plain text and may append usage on error
- **Version injection**: `utils.Version` is set via `-ldflags` at build time by GoReleaser. Local builds default to `"dev"`
- **Subcommand aliases**: list → `["list"]`, delete → `["rm"]`, describe → `["desc"]`, group → semantic alias (e.g., `workspace` → `ws`)

## Code style guidelines

### CLI usage string convention

All Cobra command `Use` fields must follow POSIX/Cobra conventions:

- **UPPERCASE**: User-supplied values (positional arguments)—`SERVER`, `COMMAND`, `SOURCE`
- **lowercase**: Literal keywords or framework tokens—`[flags]`, `[command]`
- **`[]`**: Optional—`[USER@]`, `[flags]`
- **`...`**: Repeatable—`SOURCE...`, `COMMAND...`

Examples:

```go
Use: "websh [flags] [USER@]SERVER [COMMAND]"
Use: "cp [SOURCE...] [DESTINATION]"
Use: "exec [USER@]SERVER COMMAND... [flags]"
```

### Cobra command conventions

- `Short`: Concise, starts with a verb, under 50 chars
- `Long`: Document SSH-like `user@host` syntax where supported
- `Example`: Use realistic values (e.g., `my-server`, not `[SERVER_NAME]`)

### Go declaration order

Top-level declarations within a file must follow: `const → var → type → func`. Private helper types belong in the `type` block, never between functions.

### Error handling

- golangci-lint `errcheck` is enabled—all error returns must be explicitly handled
- For deferred close calls, use the named discard pattern:

```go
// Good—errcheck satisfied
defer func() { _ = resp.Body.Close() }()
defer func() { _ = file.Close() }()

// Bad—errcheck violation
defer resp.Body.Close()
defer file.Close()
```

- For write calls where the error is intentionally ignored:

```go
_, _ = stdout.Write(output)
_ = json.NewEncoder(w).Encode(resp)
```

- Error strings should be lowercase and not end with punctuation (per Go convention / staticcheck ST1005)

### Test patterns

- Table-driven tests with `testify/assert`
- API tests use `httptest.NewServer` with a minimal `*client.AlpaconClient` pointing at `ts.URL`
- Command logic is extracted to unexported helpers (e.g., `parseExecArgs`) for direct unit testing

### Assertion conventions (testify)

- Dedicated helper over a predicate wrapped in `True/False`: `Contains`/`NotContains`, `ErrorAs`/`NotErrorAs`, `Len`, `NoError`, `Empty`. The wrapper collapses both operands to one bool and failure output loses them. `testifylint` enforces all of these but `Empty`—its `empty` checker is still disabled in `.golangci.yml` (#320), so apply that one by hand. Suffix/prefix checks have no helper, so `assert.True(t, strings.HasSuffix(...))` stays
- `require` when later lines depend on the assertion, `assert` when checks are independent. Never `require` inside an httptest handler or goroutine—`FailNow` off the test goroutine is undefined
- No message that restates the assertion (helpers print operands and error chains themselves); keep only a reason the code cannot say. `True`/`False` are the exception—they print `Should be true` and nothing else, so their message carries the operand: `"stderr line must end with a newline: %q", line`
- JSON passthrough tests: one `JSONEq` over the whole body, not per-field asserts—field lists drift and silently drop fields

### Comments

- Always write comments in English

### Writing conventions

- Use **sentence case** for all headings and descriptions (capitalize only the first word and proper nouns)
  - Good: "Execute a command on a remote server"
  - Bad: "Execute a Command on a Remote Server"
- Product and feature names:
  - **Alpacon**—the platform (proper noun, always capitalized)
  - **alpacon**—the CLI binary name (lowercase in code/commands)
  - **Websh**—the browser-based terminal feature (proper noun). Never "WebSH" or "websh" in prose. Use `websh` only in code and CLI commands
  - **Alpamon**—the agent (proper noun)
  - **Auth0**—third-party service (their capitalization)
- Deployment type terminology in user-facing messages:
  - OnPrem deployments → **"self-hosted workspaces"** (never "OnPrem" or "on-premise")
  - SaaS deployments → **"Alpacon Cloud workspaces"** (never "SaaS" or "cloud")
  - Example: `"This command is only available on Alpacon Cloud workspaces."`
- Use em-dashes (`—`) without surrounding spaces: `word—word`, not `word — word`

## Important notes

- **Go version**: see the `go` directive in `go.mod`
- **Linter**: golangci-lint v2 with errcheck, govet, ineffassign, staticcheck, unused (see `.golangci.yml`)
- **Config file**: `~/.alpacon/config.json` (dir `0700`, file `0600`). `saveConfig` writes a sibling temp file and renames it over the target—two alpacon processes can be saving a renewed access token at the same moment, and a plain truncate-and-write leaves a config the next command cannot parse. Any new writer must go through `saveConfig`
- **Alias**: `alpacon` can also be invoked as `ac`
- **File transfer**: The `cp` command lives in `cmd/ftp/` (package name `ftp`)
- **Exit codes**: `0` success, `1` general error, `2` usage error (only `work-session`, `event wait`/`watch`, and `utils.RequirePositiveInt`; every other command's own validation and Cobra parse errors still exit `1`), `3` WorkSession gate denied, `4` pending approval with the outcome still open, `5` server busy, `6` approval not granted (constants in `utils/error.go`). Keep these stable—scripts, CI, and AI agents branch on them. See README "Exit codes".
- **IAM**: `user` and `group` commands both live in `cmd/iam/`
