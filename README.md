# Alpacon CLI

[![Go Version](https://img.shields.io/github/go-mod/go-version/alpacax/alpacon-cli)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://github.com/alpacax/alpacon-cli/blob/main/LICENSE)
[![Latest Release](https://img.shields.io/github/v/release/alpacax/alpacon-cli)](https://github.com/alpacax/alpacon-cli/releases)

`Alpacon CLI` is the command-line client for [Alpacon](https://alpacon.io), the AI-native PAM. With Alpacon, humans, AI agents, and CI/CD pipelines reach and operate your entire fleet through a single identity—and every command they run is judged at runtime, recorded, and bounded by a scoped work session. Three guarantees:

1. **A gate, not a credential.** After login, a **work session** is the first thing required—nothing reaches your servers without one. Sessions are scoped (servers, commands, time window).
2. **Damage containment.** Every command is judged at runtime against the session's scope. If a credential leaks or an AI client is compromised, what the attacker can do is bounded by the session, not by what the credential could touch on its own.
3. **One audit shape.** Everything inside a session is recorded—same timeline whether the actor is human, AI agent, or CI/CD pipeline.

This CLI lets you drive your Alpacon workspace from the terminal: open a work session, then Websh into a server, exec remote commands, transfer files, create TCP tunnels, and manage API tokens with command/server/file ACLs. Login is browser-based (OAuth + MFA); everything else stays in the terminal. Built for engineers, AI coding agents (Claude Code, GitHub Copilot, Cursor, Codex CLI, Gemini CLI), and CI/CD platforms.

## Architecture

- **Alpacon Server**—the AI-native PAM control plane. Web console with simple OAuth + MFA login. Centralized RBAC, runtime command judgment, session recording, and 100% audit. Sign up at [alpacon.io](https://alpacon.io).
- **[Alpamon](https://github.com/alpacax/alpamon)**—open-source agent installed on managed servers. Outbound-only connection (no inbound ports, no firewall changes); enforces server-side decisions locally.
- **Alpacon CLI** (this repository)—command-line client for your Alpacon workspace.

## Documentation

For production usage, see the [official documentation](https://docs.alpacax.com/reference/cli/). This README is the engineering / contribution guide.

## Installation

> [!IMPORTANT]
> Building from source is for development. For production, use the package managers below or pre-built binaries from [Releases](https://github.com/alpacax/alpacon-cli/releases).

### macOS (Homebrew)
```bash
brew tap alpacax/alpacon
brew install alpacon-cli
```

### Linux (Debian / Ubuntu)
```bash
curl -s https://packagecloud.io/install/repositories/alpacax/alpacon/script.deb.sh?any=true | sudo bash
sudo apt-get install alpacon
```

### Linux (RHEL / Rocky / AlmaLinux)
```bash
curl -s https://packagecloud.io/install/repositories/alpacax/alpacon/script.rpm.sh?any=true | sudo bash
sudo yum install alpacon
```

### Windows
Download the latest `.zip` from [Releases](https://github.com/alpacax/alpacon-cli/releases) and add the binary to your `PATH`.

### Docker
```bash
docker run --rm -it alpacax/alpacon-cli version
```

### Build from source
```bash
git clone https://github.com/alpacax/alpacon-cli.git
cd alpacon-cli
go build && sudo mv alpacon-cli /usr/local/bin/alpacon
```

## Quick start

```bash
# 1. Check current login + workspace.
#    Run 'alpacon login' or 'alpacon workspace switch' if not logged in or in the wrong place.
$ alpacon

# 2. Confirm identity and whether a work session is required.
$ alpacon whoami

# 3. Open a scoped work session (interactive auth only).
$ alpacon work-session create \
    --purpose "describe the task" \
    --scope command,websh \
    --server <server> \
    --use --wait

# 4. Operate within the session.
$ alpacon websh <server>
$ alpacon exec <server> "uptime"
$ alpacon cp ./file.txt <server>:/tmp/
$ alpacon tunnel <server> -l 9000 -r 8082
```

CI/CD and API automation use token auth, which bypasses work sessions:

```bash
$ alpacon login <URL> -t <TOKEN_KEY>
$ alpacon exec <server> "..."
```

See `alpacon work-session --help` for session lifecycle, gating, and error codes.

## Login

```bash
$ alpacon login                                  # browser OAuth (default)
$ alpacon login --workspace my-ws --region us1   # cloud workspace by name/region
$ alpacon login myws.us1.alpacon.io              # by URL
$ alpacon login alpacon.example.com              # self-hosted
$ alpacon login <URL> -t <TOKEN_KEY>             # API token
$ alpacon login --no-browser                     # CI / headless (or ALPACON_NO_BROWSER=1)
$ alpacon logout
```

Successful login writes `~/.alpacon/config.json` containing the workspace URL, API token, and token expiry (~1 week). Re-login reuses the stored workspace URL unless one is provided.

For Auth0 and MFA authentication the CLI opens the auth URL in your default browser; this is skipped automatically in SSH sessions and headless environments. To force it off, use `--no-browser` or set `ALPACON_NO_BROWSER=1`. The same env var also suppresses MFA browser prompts triggered by other commands.

## Commands

Run `alpacon --help` for the full command list. Common workflows below.

### Servers
```bash
$ alpacon server ls
$ alpacon server describe <server>
$ alpacon server create                          # interactive: prompts for name,
                                                 # platform (debian/rhel/darwin/windows),
                                                 # and authorized groups
$ alpacon server rm <server>
```

### Websh (terminal in your shell)
```bash
$ alpacon websh <server>
$ alpacon websh root@<server>
$ alpacon websh -u admin -g developers <server>
$ alpacon websh --share <server>                 # share via temporary link
$ alpacon websh join --url <SHARED_URL> --password <PASSWORD>
```

### Remote command execution
```bash
$ alpacon exec <server> "<cmd>"
$ alpacon exec root@<server> "docker ps"
$ alpacon exec -u admin -g developers <server> "..."
$ alpacon exec --env="KEY=VALUE" <server> "echo $KEY"
```

Flags go before the server name; everything after is the remote command.

### File transfer
```bash
$ alpacon cp ./local.txt <server>:/home/user/
$ alpacon cp <server>:/home/user/file.txt .
$ alpacon cp -u admin -g developers <SOURCE> <DESTINATION>
$ alpacon edit <server>:/etc/nginx/nginx.conf    # open a remote file in your local editor
```

`<server>:<path>` denotes a remote target. Saving in `edit` overwrites the remote file; ownership and permissions may be reset by server policy. `edit` only opens existing remote files—it downloads first, so it won't create a new one. `--editor` is tokenized without a shell (the file path is appended as the last argument), so shell syntax such as pipes (`|`), redirections (`>>`), or `&&` won't work.

### TCP tunneling
```bash
$ alpacon tunnel <server> -l 9000 -r 8082
$ alpacon tunnel prod-db -l 5432 -r 5432 -- psql -h 127.0.0.1 -p 5432 -U app appdb
$ alpacon tunnel prod-k8s -l 6443 -r 6443 -- kubectl --server=https://127.0.0.1:6443 get pods
```

`--` separates the tunnel command from the inner command. `alpacon tunnel` does not auto-detect app ports—pass `127.0.0.1:<LOCAL_PORT>` explicitly.

### Work sessions
```bash
$ alpacon work-session ls
$ alpacon work-session current
$ alpacon work-session use <session-id>          # set active session
$ alpacon work-session use --unset
$ alpacon work-session approve <session-id>      # superuser
$ alpacon work-session reject <session-id>       # superuser
$ alpacon work-session revoke <session-id>       # superuser
```

Override the active session per command with `--work-session <id>` or `ALPACON_WORK_SESSION=<id>`. Resolution order: `--work-session` flag > env var > active session.

### Identity (users, groups)
```bash
$ alpacon user ls
$ alpacon user describe <username>
$ alpacon user create / update / rm
$ alpacon group ls
$ alpacon group member add --group <group> --member <user> --role <role>
$ alpacon group member rm --group <group> --member <user>
```

### API tokens
```bash
$ alpacon token create -n <name> --expiration-in-days=7
$ alpacon token ls
$ alpacon token rm <token-id-or-name>
$ alpacon login <URL> -t <TOKEN_KEY>
```

### Token ACLs
Each API token gets three independent **deny-by-default** ACL types—`command` (which shell commands the token can run via websh/exec), `server` (which servers it can reach), and `file` (which file paths it can read/write via cp). A bare token can do nothing until at least one ACL of each relevant type is granted; this is how `damage containment` is enforced on the token-auth path (`work session` plays the same role on the interactive-auth path).

```bash
$ alpacon token acl command add my-token --command="docker *" --username=root
$ alpacon token acl server  add my-token --servers web-01,web-02
$ alpacon token acl file    add my-token --path "/home/deploy/*" --action upload
$ alpacon token acl <type> ls     my-token
$ alpacon token acl <type> delete <acl-id>
```

### Agent (Alpamon) management
```bash
$ alpacon agent restart  <server>
$ alpacon agent upgrade  <server>
$ alpacon agent shutdown <server>
```

### Logs and audit
```bash
$ alpacon log <server> --tail=10
$ alpacon audit <filters>                        # workspace audit log
```

### More commands

Run `alpacon --help` for the full list, or `alpacon <command> --help` for details on any command.

## Exit codes

| Code | Meaning |
|------|---------|
| `0`  | Success |
| `1`  | General error (network failure, server error, etc.) |
| `2`  | Usage error (invalid flags or arguments) |
| `3`  | WorkSession gate denied—the active session does not authorize this action |

## Contributing

```bash
git clone https://github.com/alpacax/alpacon-cli.git
cd alpacon-cli
go build
go test ./...
```

### End-to-end tests against a live workspace

`sample_test_cli.sh` exercises the major commands (server lookup, exec, websh, cp, tunnel) against a real Alpacon workspace. Copy it, fill in the workspace URL and target server at the top, and run:

```bash
cp sample_test_cli.sh test_cli.sh
$EDITOR test_cli.sh                              # set WORKSPACE_URL, SERVER_NAME
chmod +x test_cli.sh && ./test_cli.sh
```

Bug reports and feature requests welcome at [GitHub Issues](https://github.com/alpacax/alpacon-cli/issues).

## License

[MIT License](LICENSE). Copyright © 2026 AlpacaX Inc.
