# GitHub Copilot Instructions

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

## Code review guidelines

- Cobra `Short` descriptions should be concise (under 50 chars) and start with a verb
- Cobra `Long` descriptions should document SSH-like `user@host` syntax where supported
- Cobra `Example` blocks should use realistic server names (e.g., `my-server`, not `[SERVER_NAME]`)
- List commands should project API responses into `*Attributes` structs for `utils.PrintTable()`
- Comments must be written in English
