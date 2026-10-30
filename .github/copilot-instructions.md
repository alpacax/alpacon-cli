# GitHub Copilot instructions

This repository is the Alpacon CLI (`alpacon`), built with Go and [Cobra](https://github.com/spf13/cobra).

## CLI usage string convention

All Cobra command `Use` fields must follow POSIX/Cobra conventions:

- **UPPERCASE** for user-supplied values (positional arguments): `SERVER`, `COMMAND`, `SOURCE`
- **lowercase** for literal keywords or framework tokens: `[flags]`, `[command]`
- **`[]`** for optional: `[USER@]`
- **`...`** for repeatable: `SOURCE...`, `COMMAND...`
- **`[flags]`**: omit it, since Cobra appends it to the end of the usage line. Write it only where flags must not come after the arguments, so the appended position would be wrong. `websh` and `exec` parse their own flags and treat every word after the server name as the remote command; `tunnel` hands every word after `--` to the local command. These put `[flags]` before those arguments, with a one-line comment beside the `Use` saying why. Never spell individual flags into `Use` either: it lists positional arguments only, and the flag list with its `(required)` notes already says which flags a command needs

Examples:

```go
Use: "websh [flags] [USER@]SERVER [COMMAND...]"
Use: "cp SOURCE... DESTINATION"
Use: "exec [flags] [USER@]SERVER [--] [COMMAND...]"
Use: "tunnel [flags] SERVER [-- COMMAND...]"
Use: "records SESSION_ID"
```

## Subcommand alias convention

- ls → `Aliases: []string{"list"}`
- delete → `Aliases: []string{"rm"}`
- describe → `Aliases: []string{"desc"}`
- Group commands may have semantic aliases (e.g., `workspace` → `ws`, `server` → `servers`)
- **Group commands**: `RunE` returns `utils.RequireSubcommand(cmd)`, which prints the help and returns a usage error (exit 2) when no subcommand is given
- **Leaf commands**: `Run` + `utils.CliErrorWithExit` (exit 1), or `utils.CliUsageErrorWithExit` (exit 2) for a flag or argument the command rejects—the message is the command's own; an error returned from `RunE` reaches `cmd.Execute()`, which prints it behind a generic "While executing the command:" prefix and exits 1, or exits 2 without the prefix when it carries `utils.MarkUsageError`

## Code review guidelines

- Cobra `Short` descriptions should be concise (under 50 chars) and start with a verb
- Cobra `Long` descriptions should document SSH-like `user@host` syntax where supported
- Cobra `Example` blocks should use realistic server names (e.g., `my-server`, not `[SERVER_NAME]`)
- List commands should project API responses into `*Attributes` structs for `utils.PrintTable()`
- Comments must be written in English

## Public repository

This repository is public, and so is everything in it: comments, test fixtures, assertion messages, scripts, help text, README.md, and this file.

- Never reference private AlpacaX internals: names of private repositories and their source paths, private function, class, or setting names, commit hashes, issue links (`alpacax/<private-repo>#N`), internal decision-record or design-doc numbers, the server's framework or configuration (rate-limit values included), non-public regions or hosts, internal workspace names, or people's names. Before naming another `alpacax` repository, check `gh repo view alpacax/<name> --json visibility`
- Describe the behavior the CLI relies on and name only the wire contract it uses: status codes, error code strings, JSON fields, routes, and headers. Write "the server returns 402 with `gate: plan`", not where the server decides it
- When a rule came from an internal document, state the rule in the comment instead of citing the document
- Test fixtures use the advertised regions (`us1`, `ap1`), an obviously fictional region such as `zz9`, and neutral names such as `my-server` or `alice`
- A bare `#N` refers to this repository's own issues. Public components such as Alpamon, and component names a user has to act on, may be named

## Exit codes

`alpacon` uses a stable exit code convention:

- `0` success
- `1` general error—the command line was valid and the command failed (environment, server, network)
- `2` usage error—a flag or argument the command rejected, in Cobra's parse or in the command's own validation, so editing the command line fixes it (`utils.ExitCodeUsageError`). These prompts check for a terminal first and exit `2` in a non-interactive run: `--yes`, `server create`'s `--platform` and `--token`, `user invite`'s email, `login`'s HOST, the `work-session create` and `extend` flags, `edit`'s `--force`; any other prompt reads stdin and exits `1` on no input, even where a flag would have answered it. Report a usage error with `utils.CliUsageErrorWithExit`
- `3` WorkSession gate denied (`utils.ExitCodeWorkSessionDenied`)
- `4` Pending human approval, outcome still open (`utils.ExitCodePendingApproval`)
- `5` Server busy with active user work (`utils.ExitCodeServerBusy`)
- `6` Approval not granted—settled without the grant (`utils.ExitCodeNotApproved`)
- `7` Purpose required—an agent's command held while the gate asks what it is for, answerable by the caller rather than by an approver (`utils.ExitCodePurposeRequired`)
- `8` A newer release exists (only `alpacon update --check`, `utils.ExitCodeUpdateAvailable`)

Keep these stable—scripts, CI, and AI agents branch on them. See README "Exit codes".

## Release workflow conventions

- Every `uses:` in `.github/workflows/` that names another repository is pinned to a 40-character commit SHA, with the version in a trailing comment. The one local reference, `uses: ./.github/workflows/build-and-test.yaml`, runs at the calling commit and has nothing to pin: `uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0`
- Never replace a pinned SHA with a tag. A tag is a ref its own repository can move—`v6.1.0` no less than `v6`—so a moved tag runs new code inside our release build with nothing in our diff to show it. A SHA is the hash of the commit it names and cannot come to mean different content
- Keep the trailing version comment. It is the only thing that says which version a SHA is, and it is what Dependabot rewrites when raising a pin. `.github/dependabot.yml` has it check the actions and the Go modules weekly, each ecosystem grouped into one PR with a Conventional Commit prefix
- Raising a pin means changing both the SHA and its comment together. Resolve the SHA rather than guessing it: `gh api repos/OWNER/REPO/commits/vX.Y.Z --jq .sha`. The one exception is `danielmundi/upload-packagecloud` (used four times in `.github/workflows/release.yaml`, the only action that receives `PACKAGECLOUD_TOKEN`): it has exactly one tag, `v1`, and no upstream commit since 2021-08, so there is no newer version to resolve and Dependabot has nothing to raise it to. Its pin stays where it is; the follow-up is replacing the action with a direct call to the PackageCloud upload API rather than raising the pin
- The `goreleaser` job attests every archive and package it builds via `actions/attest-build-provenance`, which is why it holds `id-token: write` and `attestations: write`. It also attests the Docker image, by the digest read back from the registry after GoReleaser pushes it. Do not drop either permission
- Jobs declare only the permissions they use; the workflow's top-level default is `permissions: {}`. Add a permission to the one job that needs it, never to the top level

## Writing conventions

- Use **sentence case** for all headings and descriptions (capitalize only the first word and proper nouns)
- Product and feature names:
  - **Alpacon**—the platform (proper noun, always capitalized)
  - **alpacon**—the CLI binary name (lowercase in code/commands)
  - **Websh**—the browser-based terminal feature (proper noun). Never "WebSH" or "websh" in prose. Use `websh` only in code and CLI commands
  - **Alpamon**—the agent (proper noun)
  - **Auth0**—third-party service (their capitalization)
- Use em-dashes (`—`) without surrounding spaces: `word—word`, not `word — word`
