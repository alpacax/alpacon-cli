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
# With race detection and a shuffled order (same as CI); shuffling is what
# catches a test that only passes after its neighbor
go test -race -v -shuffle=on ./...
```

The Windows installer has its own suite. `.github/workflows/install-script-tests.yaml` runs three jobs: Pester on both PowerShell editions, PSScriptAnalyzer on Windows PowerShell alone, and an end-to-end job that installs on a real runner, re-runs it after emptying both the session and the user PATH, reinstalls after the binary is deleted but the version marker survives, pins a version, and checks that a locked binary is refused. `release.yaml` calls that workflow before GoReleaser publishes anything. To run the same checks by hand on a Windows machine:

```powershell
# The same script CI runs: it installs the pinned Pester, runs the suite and
# reads the result, since Invoke-Pester throws nothing when discovery fails or
# finds no tests. powershell is Windows PowerShell 5.1, pwsh is PowerShell 7.
# Bypass is for the runner file, not for install.ps1: a Windows client defaults
# to an execution policy that refuses to load a script file at all.
powershell -NoProfile -ExecutionPolicy Bypass -File .github/scripts/run-installer-tests.ps1
pwsh -NoProfile -ExecutionPolicy Bypass -File .github/scripts/run-installer-tests.ps1

Install-Module PSScriptAnalyzer -RequiredVersion 1.24.0 -Force -SkipPublisherCheck -Scope CurrentUser
# Import the pinned version: auto-loading picks the highest one on the machine.
Import-Module PSScriptAnalyzer -RequiredVersion 1.24.0 -Force
$found = @('install.ps1', 'install.Tests.ps1', 'PSScriptAnalyzerSettings.psd1',
  '.github/scripts/run-installer-tests.ps1' |
  ForEach-Object { Invoke-ScriptAnalyzer -Path $_ -Settings ./PSScriptAnalyzerSettings.psd1 })
if ($found.Count -gt 0) { $found; throw 'PSScriptAnalyzer reported findings' }
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
pkg/                 # Internal packages (cert, selfupdate, tunnel)
utils/               # Shared utilities (output, prompts, errors, SSH parsing)
install.ps1          # Windows installer, published as a release asset
install.Tests.ps1    # Pester suite for the installer
PSScriptAnalyzerSettings.psd1  # Lint rules for the installer
```

`install.ps1` is delivered through `releases/latest/download/install.ps1` and is run by `irm | iex`, which executes it in the user's own shell. Its functions and `$script:` variables land in that shell and there is no way around it, but no preference may: `Set-StrictMode`, `$ErrorActionPreference`, and `$ProgressPreference` all live inside `Invoke-AlpaconInstall`, and a test walks the syntax tree to keep them there. The file stays pure ASCII because Windows PowerShell 5.1 reads a BOM-less file as ANSI while a BOM would reach `iex` as a stray U+FEFF. The archive and checksum file names are spelled out by hand in the script, so they are pinned to `.goreleaser.yaml`'s templates by a test as well. What the script installed is recorded in `installed-version.txt` beside the binary, because reading the version out of `alpacon version` would reach the network on every install. Both exits from `Invoke-AlpaconInstall`—the one that installs and the one that finds the binary already current—hand every PATH decision to `Set-AlpaconPath`, so a re-run repairs a user PATH entry that something else wiped out: another installer, the 2047-character truncation of the old environment variable editor, a hand edit. Dropping that call from the skip branch reads like removing dead work, and it is what left the README's promise of a PATH entry unkept for the one user who re-runs the one-liner to get that entry back (#426).

`selfupdate.syncVersionMarker` keeps that record honest from the Go side: a marker an update left behind would send the next install against a version that is no longer on disk, skipping a pinned reinstall or reporting an upgrade it never made. It rewrites the file whichever way `alpacon update` ends—after a replace, and on a run that finds nothing to install, which is what repairs a marker stranded by an update killed before it got there—and creates one nowhere, since a marker beside a binary the script never placed would claim an install it does not own.

### Key patterns

- **Command registration**: All commands are registered in `cmd/root.go` via `RootCmd.AddCommand()`
- **API layer**: Each `cmd/` package calls corresponding `api/` package for HTTP requests
- **SSH-like syntax**: `websh`, `exec`, `cp` support `user@host` syntax via `utils.ParseSSHTarget()`
- **Error handling**: Common errors (MFA required, username required) are handled via `utils.HandleCommonErrors()` with retry callbacks. `mfa.ErrorCallbacks(ac, retry)` builds the standard callback set for a command that already holds a client—use it instead of writing the whole set out again. Commands whose callbacks genuinely differ construct `utils.ErrorHandlerCallbacks` themselves: `tunnel` creates its client lazily and takes the legacy retry path; `server` needs no username handling; a workspace-level change needs none either and opens a workspace-scoped MFA link, which `mfa.WorkspaceErrorCallbacks(ac, retry)` builds and every caller—`cmd/workspace`'s two update commands and `cmd/iam`'s `user role grant`/`revoke`—calls directly. Its link is the right one for anything workspace-wide: a role binding names no server, so `mfa.ErrorCallbacks` would hand `GetMFALinkByServerName` an empty name. `utils` cannot host the helper—`api/mfa` and `api/iam` both import `utils`, so `utils` importing either back would be an import cycle
- **Workspace identity lives on the client**: `NewAlpaconAPIClient()` pins `BaseURL` and `WorkspaceName` from one `config.LoadConfig()`, and everything below `cmd/`—an MFA link above all—reads `ac.WorkspaceName`. A second read can name a workspace the request is no longer going to: another shell's `alpacon ws use` rewrites the file mid-command, and `alpacon edit` holds a client across a whole editor session. `work-session create --wait --use` blocks on a human approver before `RunUseSession` files the session through `config.SetActiveWorkSessionFor(ac.WorkspaceName, id)`; plain `SetActiveWorkSession` keys off the file's current name and belongs only to a caller with no client, `--unset` (#403). `api/config_boundary_test.go` enforces this over the AST, so a comment naming a reader is not an offender, an aliased import is, and a dot import is rejected on sight. Its reach is `api/` and the reads written there: a helper package that `api/` calls to reach config stays green, and so does a read added under `client/`, the package that holds the pin (#404). What it bars is a named list: `LoadConfig` plus `IsSaaS`, `GetActiveWorkSession`, and `ResolveAuthMethod`, since each one calls it inside. Every other `config` symbol passes—the writes (`CreateConfig`, `DeleteConfig`, `SaveRefreshedAuth0Token`, `SetActiveWorkSession`, `SwitchWorkspace`) and the device-id helpers `api/auth0` calls (`IsValidDeviceID`, `GetOrCreateDeviceID`), which answer nothing about the workspace. The four barred names are also referenced at compile time from the test file, so renaming one breaks the build rather than leaving the walk nothing to find and the guard passing with nothing checked
- **Custom flag parsing**: `websh` and `exec` use `DisableFlagParsing: true` and parse flags manually. `exec` supports `--` separator for remote command flags
- **Shared command execution**: `exec.RunCommandWithRetry()` wraps `event.RunCommand()` + `HandleCommonErrors()` with MFA/retry logic. Used by both `exec` and `websh`
- **Poll pacing**: `utils/poll.go` is the one place that paces the CLI's wait loops, with one carve-out: the two MFA step-up polls (`api/mfa`'s `stepUpForSudo` and `api/event`'s `pollMFACompletion`) keep a fixed 500ms ticker. They wait on a person finishing MFA in a browser inside one 60s window, so widening the tail would push the worst-case detection lag from half a second to five and eat the buffer `defaultMFAPollTimeout` keeps over the server's pending-grant expiry—and the quota the widening exists for is a service token's, which an interactive step-up never spends. `NextPollTick(tick, elapsed)` widens the gap as a wait ages—once elapsed passes 10x the base tick it widens to 5x, and once it passes 60x it widens to 10x. The reason is alpacon-server's default service-token quota of 1000 requests/hour: a fixed 1s tick burns through that in about 17 minutes, and past that point every freed slot just goes to a request that gets throttled again. `NextPollBackoff(tick, attempt, retryAfter)` sets the gap after a failed poll—a server-sent `Retry-After` wins, but is still capped at `PollMaxBackoffTick` (60) times the base tick, same as the exponential fallback used when the server sends none. `MaxConsecutivePollFailures` (5) ends a wait once that many polls in a row fail, rather than let it keep running toward a result nobody will read—a 429 is exempt, since it says the server is answering and the throttle budget is what bounds it. `ThrottleBudget` (`NewThrottleBudget`, `Extend`, `WarnThrottled`, `ShouldWarn`, `Reset`) is what a 429 spends to push a wait's deadline out instead of starving it: an extension is granted only while the time spent so far is still under the wait's own timeout, so the grant that carries the total past it is taken whole and nothing follows it, and the extension count is capped at `PollMaxThrottleExtensions` (60). `Reset` is the progress signal, and only a status change counts as progress—reading the same pending status again re-earns nothing, or any non-429 response would refill the whole allowance and leave the wait no ceiling. `WarnThrottled` is what every wait loop calls on a 429 so the notice reads the same everywhere and prints once per throttled stretch, `ShouldWarn` being the once-per-stretch primitive under it. `ThrottleCeiling` states the bound that follows, one timeout plus one allowance plus one overshoot, for the wait loops' own tests. `exec --wait` uses this pacing on a sudo denial: instead of re-submitting the command, it polls the denied command's own detail for `sudo_grant_status`, since the denied command already ran and finished—re-subscribing to it would only replay the old, denied output. Once `sudo_grant_status` reads `authorized`, `runAfterApproval` runs the command fresh rather than resuming the finished job
- **Browser auto-open**: `utils.OpenBrowser()` opens auth URLs with SSH/headless detection, cross-process debounce (`~/.alpacon/.browser_lock`), and `ALPACON_NO_BROWSER` env var opt-out
- **Output format flag**: `--output` persistent flag on `RootCmd` (`table` | `json`, default `table`), bound to `utils.OutputFormat` global in `cmd/root.go`. No short form—`-o` is reserved for subcommand-local `--out` flags (e.g., `cert download -o path`). `--output json` produces pretty-printed JSON (2-space indent) on stdout via `utils.PrintTable()` / `utils.PrintJson()`; default preserves existing behavior (table for list commands, pretty JSON for detail commands). Empty/nil slices emit `[]`
- **Table output**: API response → `*Attributes` struct projection → `utils.PrintTable()`. All list commands follow this pattern
- **Pagination**: `api.FetchAllPages[T]` (generics) handles all pagination internally. `cmd/` layer never sees pagination. For a list bounded by `--tail`, use `api.FetchPagesUpTo[T]` (PageNumber) or `api.FetchCursorPages[T]` (Elasticsearch cursor) so only the requested pages are fetched—the server caps `page_size` at 100, so a single request cannot satisfy a larger `--tail`
- **`--tail` semantics**: `--tail N` means the newest N entries, never the physical end of a descending list. Endpoints that sort newest first satisfy it from the first page; a helper that slices `list[len-N:]` would return the oldest N instead
- **Dual auth tokens**: the access token (Auth0 Bearer JWT) takes priority; `Token` (legacy API key) is fallback. Set in `client.setHTTPHeader()`, which reads the token through `ac.AccessToken()`—the field itself is unexported and `SetAccessToken()` is the only write path, both under `tokenMu`. `client/token_boundary_test.go` enforces that over the AST: a reference to the field anywhere but those two accessor bodies fails, whether it is a selector or a composite-literal key, which is why `NewAlpaconAPIClient` plants the token through the setter rather than in its own struct literal. An accessor body is matched by receiver as well as by name, so a plain function called `AccessToken` inherits none of the pass
- **Stale-token renewal**: `NewAlpaconAPIClient()` refreshes an expired access token once, at construction—which never fires again for a client held through a long wait. `client.sendRequest()` therefore renews mid-flight: a 401 whose body is a JSON object carrying no error code is the only 401 a fresh token could plausibly move, so it runs the refresh-token grant and replays the request once. The server's Auth0 authenticator returns no user on every bearer rejection, whether it absorbs an exception or declines outright, collapsing an expired token and every other one alike into DRF's uncoded `NotAuthenticated`—so the empty code slot is not proof of expiry, only the absence of a stated refusal. A coded 401 names what it wants (MFA required, IP not allowed, token ACL) and a new token is not it, so it is never renewed on. The JSON-object test is what keeps a gateway out of the grant—a proxy, a WAF or an mTLS gate answers 401 with an HTML page or nothing, and no token this process can obtain moves that answer. Only a bearer request qualifies—a legacy API key or a service token has no refresh token behind it—and only a body `net/http` can rewind is replayed. Concurrent requests sharing one expiry spend one grant: it runs under `refreshMu`, never under the `tokenMu` that `AccessToken()` takes to read the token—the grant is unbounded network I/O, and holding that lock across it would stall every request the client has in flight
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

- Table-driven tests with `testify/assert`. Every struct case table runs its cases under `t.Run` so a failure names the case and `-run 'TestX/case'` can select one. A loop that walks a list of inputs inside a single case is not a case table and stays as it is. `-run` matches a regex against the name Go has already rewritten, spaces included—it turns them into underscores—so a case name carries no parentheses or other regex metacharacters, and never an empty string, which Go renames to `#00`
- Every test calls `t.Parallel()` first unless it touches process-wide state or reads a package-level Cobra command. Process-wide state is `t.Setenv` and `t.Chdir` (Go panics when a test calls either alongside `t.Parallel()`, in whichever order), an `os.Stdout`/`os.Stderr` swap including `testutil.CaptureOutput`, `utils.OutputFormat`, and any package-level seam a test reassigns. There are about eighteen such seams and `runPresenceStepUp`, `tunnelFlags` and `refreshAccessToken` are only three of them, so what decides is whether the assignment outlives the test, not whether the name appears here. Cobra commands are out because `Commands()` sorts the child list in place and `Find()` builds the merged flag set lazily, so even a read-only test races with its neighbor. A helper-process test stays serial as well: the child reaches an `os.Exit`, and the parent's own run of it is a guard that returns at once. Serial tests run to completion before the parallel ones start, so a serial test may reassign a global as long as it restores it—that ordering holds within one package's test binary, and `go test ./...` still runs other packages' binaries beside it
- A test whose subject is a timing window stays serial too: sharing a core with parallel neighbours widens every wait, which narrows the window the test is watching and costs it the detection it exists for
- API tests use `httptest.NewServer` with a minimal `*client.AlpaconClient` pointing at `ts.URL`
- Command logic is extracted to unexported helpers (e.g., `parseExecArgs`) for direct unit testing

### Assertion conventions (testify)

- Dedicated helper over a predicate wrapped in `True/False`: `Contains`/`NotContains`, `ErrorAs`/`NotErrorAs`, `Len`, `NoError`, `Empty`. The wrapper collapses both operands to one bool and failure output loses them. `testifylint` enforces all of these, `Empty` included (#320 turned its `empty` checker on: `Empty` also accepts `nil`, `0` and an empty slice, and the function signature under test pins the type anyway). Suffix/prefix checks have no helper. Where the whole path is known, `assert.Equal` on it beats `assert.True(t, strings.HasSuffix(...))`—it prints both operands and pins the head of the path too; only a genuinely unknowable head keeps `HasSuffix`, with the operand in the message
- `require` when later lines depend on the assertion, `assert` when checks are independent. Never `require` or `t.Fatalf` inside an httptest handler or goroutine—`FailNow` off the test goroutine is undefined, and `go-require` only sees the testify form, so the `t.Fatalf` shape has no linter behind it. A handler reports with `t.Errorf` or `assert`, answers 500, and returns
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
- **Config file**: `~/.alpacon/config.json` (dir `0700`, file `0600`). `saveConfig` writes a sibling temp file and renames it over the target—two alpacon processes can be saving a renewed access token at the same moment, and a plain truncate-and-write leaves a config the next command cannot parse. Any new writer must go through `saveConfig`. Anything already wider than those modes is narrowed in place before it is read or written; `config/permissions.go` decides what to narrow and `config/permissions_unix.go` holds the guards it refuses on, with the platform-specific opens in `config/permissions_darwin.go` and `config/permissions_linux.go`. Each chmod is bound to an open handle rather than to a path, so nothing can be swapped in between. Two path-based reads are left, and neither decides a mode by itself: the pre-check in `restrictConfigDirectoryMode` that asks whether a directory permitting traversal but not reads has anything to narrow at all—safe because the platform helper behind it re-derives the mode from its own handle and only ever ANDs with `0700`—and `narrowToPublishedFileMode`'s `os.Stat` in `config/device.go`, which reads the mode a rename is about to stand in for and intersects it, so it can only take bits away. `openToNarrow` refuses a symbolic link instead of following one—which errno says a link was met is the platform's own decision, so `symlinkErrnos` is declared per OS (`ELOOP` on Linux and macOS, `EMLINK` and `EFTYPE` alongside it on the BSDs, nothing on Windows) rather than assumed: the target could be a file the user does not own, and the same run under `sudo` would take access away from whatever the link named. Reaching a file inside the directory is the one place a link is followed—`openConfigDirectory` opens `~/.alpacon` without `O_NOFOLLOW`, because pointing it at a dotfiles tree is a supported setup and refusing it would leave that installation with no device id while the same link kept handing `LoadConfig` the refresh token. Only the directory's own chmod refuses the link, which is the attack this guards: aiming a chmod at a link someone else planted. Every guard on the file itself still runs inside the directory the link resolves to, and the file is reached with `openat` on that handle, so `O_NOFOLLOW` covers the last component the link could not. `refuseUnsafeConfigFile` is what a caller must not read past: it rejects anything that is not a regular file, since `O_NONBLOCK` keeps the *open* off a named pipe but says nothing about the read behind it—`os.NewFile` hands a non-blocking descriptor to the poller and `io.ReadAll` then waits there forever—and it rejects a handle on a file this process does not own, because mode bits say nothing there: root can read anything, and a `sudo` that keeps `HOME` would otherwise take an unprivileged user's own file as root's device id. Both carry `errUnsafeConfigFile`, and that is the one narrowing failure `LoadConfig` refuses rather than warns about, since a config file another account owns names the host the CLI talks to and decides whether its certificate is checked. The chmod separately refuses a file that answers to more than one name, because `O_NOFOLLOW` says nothing about hard links and macOS lets any account link a file it can merely read; that guard sits inside the chmod callback rather than ahead of it, so a file already within the mask passes—publishing the device id leaves a second name on the new file for as long as its temporary copy lives. The mode is read back after a chmod that reported success, since a filesystem without permission bits answers the call and keeps the mode; only bits that hand another account access count as a failure, because macOS reports every file on a FAT volume as `0700`. Everything else is best effort—a read-only mount, a directory another account owns, and a modeless filesystem all refuse the chmod—so `warnUnrestricted` warns once per subject and cause and the command carries on rather than dying over a mode it cannot change. `LoadConfig` therefore still reads a `config.json` that is itself a symbolic link, which is what keeps a single config file kept in a dotfiles tree working; it re-opens the path with `openConfigForRead` and refuses what that resolves to unless it is a regular file, so a link aimed at a pipe reports an error instead of parking the command. `~/.alpacon/device_id` fails closed throughout: an MFA presence proof binds to whatever identifier `readDeviceID` returns, so it reads the handle it narrowed and treats one it could not protect as no identifier at all, leaving the server to fall back to an IP fingerprint. All of it is Unix only. Windows chmod changes the read-only attribute rather than access permissions, so nothing is repaired there, the two modes above are not enforced, and the symlink, hard-link and owner refusals are stubs—the device id is not protected on Windows either
- **Alias**: `alpacon` can also be invoked as `ac`
- **File transfer**: The `cp` command lives in `cmd/ftp/` (package name `ftp`)
- **Exit codes**: `0` success, `1` general error, `2` usage error (only `work-session`, `event wait`/`watch`, and `utils.RequirePositiveInt`; every other command's own validation and Cobra parse errors still exit `1`), `3` WorkSession gate denied, `4` pending approval with the outcome still open, `5` server busy, `6` approval not granted, `7` purpose required—an agent's command held while the gate asks what it is for, answerable by the caller rather than by an approver, `8` a newer release exists (only `alpacon update --check`) (constants in `utils/error.go`). Keep these stable—scripts, CI, and AI agents branch on them. See README "Exit codes".
- **Workflow actions are pinned by commit SHA**: every `uses:` in `.github/workflows/` that names another repository carries a 40-character SHA with the version in a trailing comment (`@924ae3a... # v6.5.0`); the one local reference, `./.github/workflows/build-and-test.yaml`, runs at the calling commit and has nothing to pin. A tag is a ref its own repository can move, `v6.1.0` no less than `v6`—and a moved tag runs new code inside our release build with nothing in our diff to show it. A SHA is the hash of the commit it names, so it cannot come to mean different content. Keep the trailing comment: it is what says which version a SHA is, and what Dependabot rewrites when raising the pin—`.github/dependabot.yml` has it check the actions and the Go modules weekly, each ecosystem grouped into one PR with a Conventional Commit prefix. The one pin nothing raises is `danielmundi/upload-packagecloud` in `.github/workflows/release.yaml`: it has had exactly one tag, `v1`, since 2021-08, so there is no newer commit to move to, and the follow-up is replacing the action with a direct call to the PackageCloud upload API. On the Go side the one bump nothing raises is a `github.com/xtaci/smux` major: the project tagged v2 in 2019 without a `/v2` module path, so Go reads `v2.0.1+incompatible` as newer than `v1.5.57` while the tree behind it predates `MaxStreamBuffer`, which `config.GetSmuxConfig` sets, and the bump does not compile—`.github/dependabot.yml` ignores it until a real `/v2` module exists. Pinning is also what narrows the one thing provenance cannot answer—an action compromised upstream is run by the real workflow and gets a valid attestation
- **Release provenance**: the `goreleaser` job attests every archive and package it builds, and the Docker image too—by the digest read back from the registry after GoReleaser pushes it, since the image has no file under `dist/` to name with `subject-path`—which is why it holds `id-token: write` and `attestations: write`. The signing certificate is minted from that job's OIDC identity and lives for minutes, so there is no key to store or leak, and the signature is recorded in a public transparency log. This is what the checksums file cannot do: both the archive and its checksum come from the same release, so one stolen release token rewrites the pair and verification still passes. `alpacon update` reads only the checksum—teaching it to verify provenance is #412
- **IAM**: `user` and `group` commands both live in `cmd/iam/`
