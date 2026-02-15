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
go test ./...
```

### Lint

```bash
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

### Comments

- Always write comments in English

## Important Notes

- **Go version**: 1.25.7 (specified in go.mod)
- **Alias**: `alpacon` can also be invoked as `ac`
- **File transfer**: The `cp` command lives in `cmd/ftp/` (package name `ftp`)
- **IAM**: `user` and `group` commands both live in `cmd/iam/`
