# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Alpacon CLI (`alpacon`, alias `ac`) is a command-line tool for interacting with the Alpacon platform. Built with Go and [Cobra](https://github.com/spf13/cobra).

## Development Commands

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

### Project Structure

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

### Key Patterns

- **Command registration**: All commands are registered in `cmd/root.go` via `RootCmd.AddCommand()`
- **API layer**: Each `cmd/` package calls corresponding `api/` package for HTTP requests
- **SSH-like syntax**: `websh`, `exec`, `cp` support `user@host` syntax via `utils.ParseSSHTarget()`
- **Error handling**: Common errors (MFA required, username required) are handled via `utils.HandleCommonErrors()` with retry callbacks
- **Custom flag parsing**: `websh` uses `DisableFlagParsing: true` and parses flags manually to support positional args after the server name
- **Table output**: API response → `*Attributes` struct projection → `utils.PrintTable()`. All list commands follow this pattern
- **Pagination**: `api.FetchAllPages[T]` (generics) handles all pagination internally. `cmd/` layer never sees pagination
- **Dual auth tokens**: `AccessToken` (Auth0 Bearer JWT) takes priority; `Token` (legacy API key) is fallback. Set in `client.setHTTPHeader()`
- **CLI output helpers**: `utils.CliError*/CliInfo*/CliWarning` all write to stderr. stdout is reserved for data output (tables, JSON)
- **Group commands**: Use `RunE` (not `Run`) to show help + return error when no subcommand is given
- **Version injection**: `utils.Version` is set via `-ldflags` at build time by GoReleaser. Local builds default to `"dev"`
- **Subcommand aliases**: list → `["list"]`, delete → `["rm"]`, describe → `["desc"]`, group → semantic alias (e.g., `workspace` → `ws`)

## Code Style Guidelines

### CLI Usage String Convention

All Cobra command `Use` fields must follow POSIX/Cobra conventions:

- **UPPERCASE**: User-supplied values (positional arguments) — `SERVER`, `COMMAND`, `SOURCE`
- **lowercase**: Literal keywords or framework tokens — `[flags]`, `[command]`
- **`[]`**: Optional — `[USER@]`, `[flags]`
- **`...`**: Repeatable — `SOURCE...`, `COMMAND...`

Examples:

```go
Use: "websh [flags] [USER@]SERVER [COMMAND]"
Use: "cp [SOURCE...] [DESTINATION]"
Use: "exec [USER@]SERVER COMMAND... [flags]"
```

### Cobra Command Conventions

- `Short`: Concise, starts with a verb, under 50 chars
- `Long`: Document SSH-like `user@host` syntax where supported
- `Example`: Use realistic values (e.g., `my-server`, not `[SERVER_NAME]`)

### Error Handling

- golangci-lint `errcheck` is enabled — all error returns must be explicitly handled
- For deferred close calls, use the named discard pattern:

```go
// Good — errcheck satisfied
defer func() { _ = resp.Body.Close() }()
defer func() { _ = file.Close() }()

// Bad — errcheck violation
defer resp.Body.Close()
defer file.Close()
```

- For write calls where the error is intentionally ignored:

```go
_, _ = stdout.Write(output)
_ = json.NewEncoder(w).Encode(resp)
```

- Error strings should be lowercase and not end with punctuation (per Go convention / staticcheck ST1005)

### Test Patterns

- Table-driven tests with `testify/assert`
- API tests use `httptest.NewServer` with a minimal `*client.AlpaconClient` pointing at `ts.URL`
- Command logic is extracted to unexported helpers (e.g., `parseExecArgs`) for direct unit testing

### Comments

- Always write comments in English

## Important Notes

- **Go version**: 1.25.7 (specified in go.mod)
- **Linter**: golangci-lint v2 with errcheck, govet, ineffassign, staticcheck, unused (see `.golangci.yml`)
- **Config file**: `~/.alpacon/config.json` (dir `0700`, file `0600`)
- **Alias**: `alpacon` can also be invoked as `ac`
- **File transfer**: The `cp` command lives in `cmd/ftp/` (package name `ftp`)
- **IAM**: `user` and `group` commands both live in `cmd/iam/`
