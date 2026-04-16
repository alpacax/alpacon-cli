# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project overview

Alpacon CLI (`alpacon`, alias `ac`) is a command-line tool for [Alpacon](https://alpacon.io)—a zero-trust infrastructure access platform that replaces SSH keys, VPNs, and bastion hosts with a single secure identity. Built with Go and [Cobra](https://github.com/spf13/cobra).

Alpacon enables teams to scale operations across servers and customer environments without per-server credentials, and provides API tokens for CI/CD pipelines and AI agents to access infrastructure safely. The CLI is designed to be used by engineers, AI coding agents (Claude Code, GitHub Copilot, Cursor, Codex CLI, Gemini CLI), and CI/CD platforms for the most AI-native infrastructure operations.

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
- **Error handling**: Common errors (MFA required, username required) are handled via `utils.HandleCommonErrors()` with retry callbacks
- **Custom flag parsing**: `websh` and `exec` use `DisableFlagParsing: true` and parse flags manually. `exec` supports `--` separator for remote command flags
- **Shared command execution**: `exec.RunCommandWithRetry()` wraps `event.RunCommand()` + `HandleCommonErrors()` with MFA/retry logic. Used by both `exec` and `websh`
- **Browser auto-open**: `utils.OpenBrowser()` opens auth URLs with SSH/headless detection, cross-process debounce (`~/.alpacon/.browser_lock`), and `ALPACON_NO_BROWSER` env var opt-out
- **Output format flag**: `--output` persistent flag on `RootCmd` (`table` | `json`, default `table`), bound to `utils.OutputFormat` global in `cmd/root.go`. No short form—`-o` is reserved for subcommand-local `--out` flags (e.g., `cert download -o path`). `--output json` produces pretty-printed JSON (2-space indent) on stdout via `utils.PrintTable()` / `utils.PrintJson()`; default preserves existing behavior (table for list commands, pretty JSON for detail commands). Empty/nil slices emit `[]`
- **Table output**: API response → `*Attributes` struct projection → `utils.PrintTable()`. All list commands follow this pattern
- **Pagination**: `api.FetchAllPages[T]` (generics) handles all pagination internally. `cmd/` layer never sees pagination
- **Dual auth tokens**: `AccessToken` (Auth0 Bearer JWT) takes priority; `Token` (legacy API key) is fallback. Set in `client.setHTTPHeader()`
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
- **Group commands**: Use `RunE` (not `Run`) to show help + return error when no subcommand is given
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

- **Go version**: 1.25.7 (specified in go.mod)
- **Linter**: golangci-lint v2 with errcheck, govet, ineffassign, staticcheck, unused (see `.golangci.yml`)
- **Config file**: `~/.alpacon/config.json` (dir `0700`, file `0600`)
- **Alias**: `alpacon` can also be invoked as `ac`
- **File transfer**: The `cp` command lives in `cmd/ftp/` (package name `ftp`)
- **IAM**: `user` and `group` commands both live in `cmd/iam/`
