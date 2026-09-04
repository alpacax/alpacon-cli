# GitHub Copilot instructions

This repository is the Alpacon CLI (`alpacon`), built with Go and [Cobra](https://github.com/spf13/cobra).

## CLI usage string convention

All Cobra command `Use` fields must follow POSIX/Cobra conventions:

- **UPPERCASE** for user-supplied values (positional arguments): `SERVER`, `COMMAND`, `SOURCE`
- **lowercase** for literal keywords or framework tokens: `[flags]`, `[command]`
- **`[]`** for optional: `[USER@]`, `[flags]`
- **`...`** for repeatable: `SOURCE...`, `COMMAND...`

Examples:

```go
Use: "websh [flags] [USER@]SERVER [COMMAND]"
Use: "cp [SOURCE...] [DESTINATION]"
Use: "exec [USER@]SERVER COMMAND... [flags]"
Use: "tunnel [SERVER] -l LOCAL -r REMOTE [flags]"
```

## Subcommand alias convention

- list → `Aliases: []string{"list", "all"}`
- delete → `Aliases: []string{"rm"}`
- describe → `Aliases: []string{"desc"}`
- Group commands may have semantic aliases (e.g., `workspace` → `ws`, `server` → `servers`)
- **Group commands**: `RunE`—returns error to trigger help when no subcommand is given
- **Leaf commands**: `Run` + `utils.CliErrorWithExit`—preserves colored output; `RunE` would print plain text and may append usage on error

## Code review guidelines

- Cobra `Short` descriptions should be concise (under 50 chars) and start with a verb
- Cobra `Long` descriptions should document SSH-like `user@host` syntax where supported
- Cobra `Example` blocks should use realistic server names (e.g., `my-server`, not `[SERVER_NAME]`)
- List commands should project API responses into `*Attributes` structs for `utils.PrintTable()`
- Comments must be written in English

## Exit codes

`alpacon` uses a stable exit code convention:

- `0` success
- `1` general error
- `2` usage error—a flag or argument rejected by `work-session`, `event wait`/`watch`, or `utils.RequirePositiveInt` (`utils.ExitCodeUsageError`); every other command's own validation and Cobra parse errors exit `1`
- `3` WorkSession gate denied (`utils.ExitCodeWorkSessionDenied`)
- `4` Pending human approval, outcome still open (`utils.ExitCodePendingApproval`)
- `5` Server busy with active user work (`utils.ExitCodeServerBusy`)
- `6` Approval not granted—settled without the grant (`utils.ExitCodeNotApproved`)

Keep these stable—scripts, CI, and AI agents branch on them. See README "Exit codes".

## Release workflow conventions

- Every `uses:` in `.github/workflows/` that names another repository is pinned to a 40-character commit SHA, with the version in a trailing comment. The one local reference, `uses: ./.github/workflows/build-and-test.yaml`, runs at the calling commit and has nothing to pin: `uses: actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16 # v6.5.0`
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
