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
brew install alpacax/alpacon/alpacon-cli
brew upgrade alpacon-cli   # update
```

### Linux (Debian / Ubuntu)
```bash
curl -s https://packagecloud.io/install/repositories/alpacax/alpacon/script.deb.sh?any=true | sudo bash
sudo apt-get install alpacon
sudo apt-get update && sudo apt-get install --only-upgrade alpacon   # update
```

### Linux (RHEL / Rocky / AlmaLinux)
```bash
curl -s https://packagecloud.io/install/repositories/alpacax/alpacon/script.rpm.sh?any=true | sudo bash
sudo yum install alpacon
sudo yum update alpacon   # update
```

### Windows
```powershell
irm https://github.com/alpacax/alpacon-cli/releases/latest/download/install.ps1 | iex
```

Installs to `%LOCALAPPDATA%\Alpacon\bin` and puts that directory on your user `PATH`—no administrator rights needed. Windows 10 and 11 carry everything it needs; on anything older, PowerShell 5.0 is the floor, since that is where `Expand-Archive` arrives. The execution policy does not have to be relaxed, because `iex` runs a string rather than a script file. A device policy that forces constrained language mode does block it, and it says so instead of failing halfway. After an install or an update, the shell you ran it in can use `alpacon` right away; terminals that were already open need a restart. Run the same command again to update, or to put that directory back on your `PATH` after something else removed it. `alpacon update` updates this install as well, since no package manager owns a binary the script placed.

The script is piped straight into `iex`, so you run it without reading it first. To look before you run, open a release on [Releases](https://github.com/alpacax/alpacon-cli/releases), download `install.ps1` and `alpacon-<version>-checksums.sha256` from it, compare the file's SHA256 against the `install.ps1` line in that checksums file, and then run the file. Both come from the same release, so this proves the download arrived intact—not that the release itself is what we meant to publish.

`irm | iex` cannot pass arguments, so pass `-Version` to pin a release, or `-Force` to reinstall the version you already have, through a script block:

```powershell
&([scriptblock]::Create((irm https://github.com/alpacax/alpacon-cli/releases/latest/download/install.ps1))) -Version 1.10.0
&([scriptblock]::Create((irm https://github.com/alpacax/alpacon-cli/releases/latest/download/install.ps1))) -Force
```

Pre-built `.zip` archives for 64-bit x86 and for ARM stay on [Releases](https://github.com/alpacax/alpacon-cli/releases). There is no 32-bit x86 build, and the installer says so rather than guessing.

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

### Updating

`alpacon update` brings the CLI to the latest release. A binary built from source carries the version `dev`, which matches no release, so the command refuses it and says so—build with GoReleaser or install a released build to use it. A binary you placed yourself is downloaded, verified against the release's SHA-256 checksums, and replaced in place; the old binary is kept beside it until the replacement succeeds—on Windows, where a running executable stays locked, it is kept as `alpacon.exe.old.<timestamp>` and removed by the next update. A binary a package manager or a version manager owns is never overwritten—the command prints how to update through that tool and exits `1`, leaving the upgrade to you. If the ownership question cannot be answered at all—`rpm -qf` takes a read lock on the rpm database, so it is killed when another package transaction holds it—the binary is left alone rather than assumed unowned, and the command says to retry once that transaction has finished. Homebrew, deb and rpm get the exact command; mise and asdf are named without one, since the command depends on how the tool was set up. While it runs it holds `.alpacon-update.lock` beside the binary, so two updates cannot replace the same file at once; the file stays there afterwards and is safe to leave alone. The command only moves forward: a build ahead of the latest release, such as a release candidate, is left alone rather than downgraded. `alpacon update --check` reports whether a newer release exists and installs nothing, exiting `8` when there is one. On Windows it also puts the install script's record of the installed version back in step, so the script and the command can be mixed without one of them acting on a version that is no longer there.

If the binary or its directory is group- or world-writable, the update says so on stderr rather than narrowing anything—re-permissioning what you set up is your call. The directory is the one that usually matters: replacing a file is a rename, which needs no permission on the file itself, so a `0755` root-owned `alpacon` inside a `0775` directory can still be swapped by anyone in that group. Close it with `chmod go-w`.

`alpacon version --output json` prints one object on stdout with `version`, `latest`, `update_available`, and `release_url`. `update_available` is `true` only when `alpacon update` has a newer release to install, so a `dev` build reports `false`. When the latest release cannot be fetched, `latest` and `release_url` are `null`, a warning goes to stderr, and the command still exits `0`—branch on `alpacon update --check` instead.

#### What the checksum and the attestation do and do not prove

`alpacon update` compares SHA-256 checksums and nothing else. That comparison proves the archive arrived intact and matches the checksums file the release publishes; it proves nothing about who published either, because both come from the same release. Anyone who could write to a release—a leaked token, a compromised account, a poisoned release workflow—could upload a trojaned archive together with a checksums file that matches it, and the update would install it. Teaching the CLI to verify the publisher is tracked in [#412](https://github.com/alpacax/alpacon-cli/issues/412).

Every release published since provenance was added to the release workflow does carry a [build provenance attestation](https://docs.github.com/actions/security-for-github-actions/using-artifact-attestations/using-artifact-attestations-to-establish-provenance-for-builds), which the update does not read but you can. The Docker image joined the attested set later than the archives and packages, so an older image carries no record where an archive from the same release does. The release workflow signs each archive and package with a short-lived certificate minted from its own GitHub identity, and the signature is recorded in a public transparency log—so a release published any other way cannot be made to verify, and one published this way leaves a record nobody can quietly remove. A release older than that carries no record rather than a bad one, and `gh attestation verify` reports it as a `404`.

To check out of band, compare `alpacon version` against the [Releases](https://github.com/alpacax/alpacon-cli/releases) page, and verify the archive yourself before installing it:

```bash
# The archive is .tar.gz everywhere but Windows, which ships .zip.
curl -LO https://github.com/alpacax/alpacon-cli/releases/download/v<version>/alpacon-<version>-<os>-<arch>.tar.gz
curl -LO https://github.com/alpacax/alpacon-cli/releases/download/v<version>/alpacon-<version>-checksums.sha256
sha256sum --check --ignore-missing alpacon-<version>-checksums.sha256

# Who built it, from which commit. Needs the GitHub CLI.
gh attestation verify alpacon-<version>-<os>-<arch>.tar.gz \
  --repo alpacax/alpacon-cli \
  --signer-workflow alpacax/alpacon-cli/.github/workflows/release.yaml
```

The Docker image carries the same attestation, keyed to the digest of the manifest GoReleaser pushed rather than to a file:

```bash
gh attestation verify oci://index.docker.io/alpacax/alpacon-cli:<version> \
  --repo alpacax/alpacon-cli \
  --signer-workflow alpacax/alpacon-cli/.github/workflows/release.yaml
```

Provenance still answers only who built the artifact, never what went into it: a dependency or an action compromised upstream is built by the real workflow and gets a valid attestation. Pinning every action to a commit SHA is what narrows that, and the workflows do.

#### If a Windows update is killed mid-replace

Windows refuses to overwrite a running executable, so the update copies the new binary in as `alpacon.exe.staged.<timestamp>`, renames the old one aside as `alpacon.exe.old.<timestamp>`, and renames the staged copy into place. A process killed between those two renames leaves no `alpacon.exe` and both of the others beside it. Nothing recovers automatically, because the binary that would run the next update is the one that went missing.

The staged copy is the new release and was already checksum-verified, so finishing the update is usually what you want:

```powershell
Rename-Item alpacon.exe.staged.<timestamp> alpacon.exe
Remove-Item alpacon.exe.old.<timestamp>
```

To go back to the version you had instead, rename `alpacon.exe.old.<timestamp>` and delete the staged copy. Either way the next update sweeps whichever one you leave.

On an install the Windows script placed, running the one-liner again is the easier way out: it finds no `alpacon.exe`, treats the directory as empty and installs the latest release. The two leftover files stay where they are, and the next `alpacon update` sweeps them.

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
    --expires-in 1h \
    --use --wait          # --wait-approval 30m waits longer (default 5m)

# 4. Operate within the session.
$ alpacon websh <server>
$ alpacon exec <server> "uptime"
$ alpacon cp ./file.txt <server>:/tmp/
$ alpacon tunnel <server> -l 9000 -r 8082

# An AI agent replaces steps 3 and 4: it drops --use and websh, which an agent session
# refuses, and names the session before the server on each command.
$ alpacon work-session create \
    --purpose "describe the task" \
    --scope command \
    --server <server> \
    --expires-in 1h \
    --requester-type agent \
    --wait
$ alpacon exec --work-session <SESSION_ID> <server> "uptime"
```

CI/CD and API automation use token auth, which bypasses work sessions for `exec`, `cp`, `edit`, and `websh <server> "<command>"`:

```bash
$ alpacon login <URL> -t <TOKEN_KEY>
$ alpacon exec <server> "..."
```

A token cannot open an interactive `websh` terminal, run `websh watch`, `websh join`, or `websh invite`, or open a `tunnel`; those need `alpacon login` without `-t` (on a machine without a browser, open the printed URL on another device).

See `alpacon work-session --help` for session lifecycle, gating, and error codes.

## Login

```bash
$ alpacon login                                  # browser OAuth (default)
$ alpacon login --workspace my-ws --region us1   # cloud workspace by name/region
$ alpacon login alpacon.example.com              # self-hosted
$ alpacon login <URL> -t <TOKEN_KEY>             # API token
$ alpacon login myws.us1.alpacon.io              # cloud direct URL (deprecated)
$ alpacon login --workspace my-ws --region us1 -t <TOKEN_KEY> # CI / automation
$ alpacon login --workspace my-ws --region us1 --no-browser   # manual login from a headless shell
$ alpacon logout
```

Successful login writes `~/.alpacon/config.json` containing the workspace target and credentials. The file is kept `0600` and its directory `0700`; anything found wider is narrowed on the next command that reads it, and a mode that cannot be changed—a read-only mount, a directory another account owns—is reported on stderr once rather than treated as fatal. A `config.json` owned by an account other than the one running the command is refused outright instead of narrowed, because it names the host the CLI talks to and whether that host's certificate is checked. Two ordinary setups arrive at that refusal without anything being wrong: `sudo` that keeps `HOME` reads your file as root, and a container given `-v ~/.alpacon:/root/.alpacon` reads a host file owned by your uid. The error names both uids, so check them before acting on it—run the command as the account that owns the file, or bind-mount the directory for a container user with the same uid. Only when the two genuinely differ is the remedy to delete the file and log in again. Browser OAuth stores access/refresh tokens and access-token expiry; `-t` stores the supplied API token. In an interactive shell, re-login prompts with the stored target as the default instead of silently reusing it; non-interactive login requires an explicit host or `--workspace/--region`.

Keeping one `config.json` in a dotfiles tree works: a symbolic link at `~/.alpacon/config.json` is followed for reads and for writes, so a token refresh replaces the file the link names rather than the link itself, and `alpacon logout` empties the tree instead of leaving the refresh token there. The link is never chmod'ed—that is what stops someone else's link from aiming a mode change at a file you do not own—and what it resolves to is held to the same two refusals as any other config file: it must be a regular file, and it must belong to the account running the command. A link is not a warning by itself either: what it resolves to is read for its mode, and the notice about permissions the CLI could not narrow appears only when that mode actually grants another account access.

Browser login also sends a device identifier to Auth0 so an MFA prompt can be bound to the installation that requested it. It is a random value generated once and reused by every workspace this installation logs in to. On its own it authenticates nothing—an attacker who knows it still has to sign in as you—but it is the value your MFA verification is bound to, and it names this installation to the identity provider on every login and token refresh, so treat it as identifying rather than harmless: keep it out of logs and bug reports. It is stored in `~/.alpacon/device_id` with owner-only permissions, and unlike the config file it fails closed: an identifier whose permissions cannot be narrowed, or that turns out to be a link, someone else's file, or anything but a regular file, is ignored with a warning and MFA falls back to a weaker network fingerprint—a separate file from `config.json`, so it survives `alpacon logout`: the identifier describes the machine, not the session, and regenerating it would invalidate MFA verifications already tied to it and prompt you again. Delete the file to reset it; the next login generates a new one. A filesystem that cannot represent Unix permissions reaches the same fallback when its own mode is wide: FAT and exFAT report a mode the CLI cannot change, which passes when it grants no other account access—macOS reports `0700` there—and is refused when it grants group or other access, as a Linux `vfat` mount does under the default `fmask`. Mounting with `fmask=0077` is what turns that back into a usable identifier.

An installation that logged in before this identifier existed holds a refresh token issued without it. If the identity provider refuses to refresh that token with the identifier attached, the CLI retries the refresh without it, so the session keeps working and MFA verification falls back to the previous behaviour until the next `alpacon login`. Set `ALPACON_DEBUG=1` to see when that retry happens.

For Auth0 and MFA authentication the CLI opens the auth URL in your default browser; this is skipped automatically in SSH sessions and headless environments. To force it off, use `--no-browser` or set `ALPACON_NO_BROWSER=1`. The same env var also suppresses MFA browser prompts triggered by other commands.

## Shell completion

The Homebrew formula and the deb and rpm packages install completion for bash, zsh, and fish, so those installs need no per-command setup. Bash completion still needs the bash-completion package, zsh needs `compinit` in your `~/.zshrc`, and a Homebrew install needs your shell to read Homebrew's completions, as [Homebrew's shell completion guide](https://docs.brew.sh/Shell-Completion) describes. For a binary downloaded directly, or for PowerShell, load the script yourself:

```bash
source <(alpacon completion bash)                                  # bash (needs the bash-completion package; add to ~/.bashrc)
source <(alpacon completion zsh)                                   # zsh (add to ~/.zshrc after compinit)
mkdir -p ~/.config/fish/completions && alpacon completion fish > ~/.config/fish/completions/alpacon.fish  # fish
alpacon completion powershell | Out-String | Invoke-Expression     # PowerShell (add to $PROFILE)
```

## Commands

Run `alpacon --help` for the full command list. Common workflows below.

### Direct API requests

`alpacon api` sends one authenticated request to the current workspace's API. ENDPOINT is a path on that workspace, with or without a leading slash. The response body goes to stdout; an HTTP error also prints `HTTP <code>` to stderr and exits 1. An MFA-required response opens the workspace MFA link and retries the request once MFA completes, like other commands—unless the request carries its own `-H 'Authorization: ...'` header, which is sent through unchanged.

An invalid endpoint, method, field, header, or input file exits `2`. Unknown flags are rejected by Cobra and exit `1`.

```bash
$ alpacon api /api/iam/users/-/
$ alpacon api -i /api/iam/users/                 # include status and response headers
$ alpacon api -F name=my-server /api/servers/servers/  # typed fields imply POST
$ alpacon api -X GET -f search=admin /api/iam/users/
$ alpacon api -X PATCH --input update.json /api/servers/servers/123/
```

Use `-f key=value` for strings, `-F key=value` for booleans, 64-bit integers, `null`, or `@file` contents. Repeated bracket keys such as `-f 'tags[]=one' -f 'tags[]=two'` build arrays; nested keys such as `-f 'filter[name]=demo'` build objects, keeping the full nested path in the query string too. With `--input`, `-f`/`-F` fields go to the query string and the file is the body. POST/PUT/PATCH default to `Content-Type: application/json` even with `--input`; `-H 'Content-Type: ...'` overrides it for a non-JSON body. `--input -` reads stdin, `-H 'Name: value'` sets a request header, `--silent` suppresses the body, and `--verbose` prints request and response headers to stderr with `Authorization`, `Proxy-Authorization`, `Cookie`, and `Set-Cookie` redacted (the same redaction applies to `-i/--include`). A response body written to a terminal is stripped of escape sequences and other control characters, tabs excepted so a TSV or column-aligned body keeps its layout; piped or redirected output is left byte-for-byte. `Host`, `Transfer-Encoding`, `Trailer`, and `Content-Length` overrides are rejected before sending the request—net/http sets `Content-Length` from the body. ENDPOINT must resolve to a path on the current workspace—an absolute URL, a network-path reference (`//host/...`), or any other host is refused; a URL appearing inside a query value, such as a callback parameter, is not rejected.

### Servers
```bash
$ alpacon server ls
$ alpacon server describe <server>
$ alpacon server create                          # interactive: prompts for name,
                                                 # platform (debian/rhel/suse/darwin/windows; suse is token-install only),
                                                 # and authorized groups
$ alpacon server rm <server>
```

### Kubernetes clusters
```bash
$ alpacon kube ls                                # aliases: k8s, clusters
$ alpacon kube describe <cluster>
$ alpacon kube ls --output json

# Write a kubeconfig context for a cluster, then use kubectl as usual
$ alpacon kube config <cluster>                  # context alpacon-<cluster>, made current
$ kubectl get pods -A

# A separate file is read only when kubectl is pointed at it
$ alpacon kube config <cluster> --work-session <ID> --kubeconfig ~/.kube/alpacon
$ kubectl --kubeconfig ~/.kube/alpacon get pods -A

# Optional: send kubectl through a local proxy
$ alpacon kube config <cluster> --proxy-port 18443
$ alpacon kube proxy <cluster>                   # --port 18443 by default; Ctrl+C to stop
```

The `kube` group appears only on workspaces whose server reports Kubernetes support. The CLI records that answer at `alpacon login` and `alpacon workspace switch`; elsewhere `kube` is hidden from help, and calling it prints a notice and exits with `1`. If support was turned on after you logged in, run `alpacon login` again.

`kube config` opens a kube session under your active WorkSession and needs an interactive login: a token from `alpacon login -t` cannot open one. Without an active WorkSession it exits with `3` and lists what to run next. It writes to the file `kubectl config` itself writes (`--kubeconfig`, else the first existing file in `KUBECONFIG` or the last one listed when none exists, else `~/.kube/config`), keeps the other entries in it, and rewrites the file in kubectl's layout. The context holds the session's token in plain text until a credential plugin replaces it, so keep the file private (`chmod 600`). The token stops working when the WorkSession ends; run `kube config` again for a new one.

`kube proxy` is optional. It listens on `127.0.0.1` and relays HTTP CONNECT only to the cluster's proxy host, without reading the traffic, so kubectl's TLS stays end to end. kubectl uses it only for a context written with `--proxy-port` set to the same port. It finds the context the way kubectl does, across every file in `KUBECONFIG` (or the one `--kubeconfig` names). One proxy runs per cluster and port.

### Websh (terminal in your shell)
```bash
$ alpacon websh <server>
$ alpacon websh root@<server>
$ alpacon websh -u admin -g developers <server>
$ alpacon websh --share <server>                 # share via temporary link
$ alpacon websh join --url <SHARED_URL> --password <PASSWORD>
```

For a terminal you opened yourself, if the connection drops for any reason other than the session ending—a restarted service, a network interruption—the terminal reconnects to the same session instead of closing, retrying up to five times with a growing delay; input typed while it is down is not delivered. A terminal joined through a shared link does not reconnect: rejoin with the link if it drops.

### Remote command execution
```bash
$ alpacon exec <server> "<cmd>"
$ alpacon exec root@<server> "docker ps"
$ alpacon exec -u admin -g developers <server> "..."

# Pass a secret with --env="KEY": the value is read from your shell, so it stays off
# the alpacon command line. Read it in rather than typing it inline, so it stays out
# of shell history too.
$ printf 'PGPASSWORD: ' && read -rs PGPASSWORD && export PGPASSWORD
$ alpacon exec --env="PGPASSWORD" <server> -- psql -h localhost -U app -c 'SELECT 1'
```

Flags go before the server name; everything after is the remote command.

Never put a secret on the command line: the server refuses the recognizable forms before the command runs. Pass it with `--env="KEY"` as shown above. The same applies to `alpacon websh` when it runs a command. See [When a command is denied](#when-a-command-is-denied) for the exact forms the server rejects and the machine-readable refusal.

#### Verified file execution
```bash
# Run /opt/deploy.sh on the server as a verified file. The content is read from
# the same path locally and sent byte-for-byte; script arguments go after --.
$ alpacon exec --file /opt/deploy.sh root@<server> -- --fast

# Read the content from another local copy, and pick the interpreter.
$ alpacon exec --file /opt/deploy.sh --file-from ./deploy.sh --interpreter /bin/sh <server>

# Run a script that exists only on the server; the CLI reads its content from there.
$ alpacon exec --file /opt/deploy.sh --from-server root@<server> -- --fast

# Write a local script to the server and run it as a verified file, in one step.
$ alpacon exec --upload --file /opt/deploy.sh --file-from ./deploy.sh root@<server> -- --fast

# Propose that an approval stay reusable for 30 days. The approver decides.
$ alpacon exec --file /opt/deploy.sh --reuse-days 30 root@<server> -- --fast
```

`--file` submits a script as structure instead of a command line: the platform hashes the bytes the reviewer sees, and the agent proves at execution time that the file it runs on the server is those bytes. The file must already exist at that path on the target server, or `--upload` writes it there first, and the local copy the CLI reads must be byte-identical, or the agent refuses to run it. A script that already exists only on the server takes `--from-server` instead: the CLI reads the file from the server as the account and group the script runs as, through the same file transfer `alpacon cp` uses, and submits those bytes, so no local copy is needed. The requester still submits the content and the agent still hashes the file at execution, so the guarantee is the same; a file that cannot be read, is empty, or exceeds 64 KB is refused before anything is submitted. `--from-server` does not combine with `--upload` or `--file-from`. When the server holds an ordinary command line it reads as a script on the target, such as `bash /opt/deploy.sh`, the pending-approval message adds the `--file --from-server` form of it. When the command line named its interpreter by a bare name, such as `bash` rather than `/bin/bash`, that form carries an `--interpreter` placeholder to replace with the interpreter's absolute path on the server, which `command -v bash` prints there. The held command stays queued and runs if approved, and the CLI cannot cancel it, so cancel it in the Alpacon console (Commands) before submitting that form, or the script waits on two approval requests. The form is left out when the command carried `--env`, which the file lane cannot take. With `--upload` the CLI reads the local script once, writes those bytes to the `--file` path on the server, and then submits the same bytes, so the file the reviewer sees is the file written on the server, and the agent still hashes it at execution and refuses a mismatch. The file is written as the account and group the script runs as (`-u` or `USER@`, `-g`, or the server's default account when neither is given). A new file takes that account's umask and any missing directories are created; the interpreter reads the file, so it needs no execute bit. With `--upload` the path must be clean, with no `.` or `..` and no repeated or trailing slash, so the file written is the file the agent opens. A file already at that path is refused unless `--overwrite` is given, and an overwritten file keeps its owner and mode. A failed write stops the run before anything is submitted; a successful write stays on the server even when the submission is then refused. The pending-approval re-run hint leaves out `--upload` and `--overwrite`, since the file is already on the server by then. Both the path and `--interpreter` (default `/bin/bash`) must be absolute; the content is limited to 64 KB; `--env` does not combine with `--file`. Script arguments are recorded with the command, shown to the reviewer, and repeated in the re-run hint, so a secret goes in a file or the environment the script reads at run time, never in an argument.

An approval binds exactly those bytes, on that server, as that account, with those arguments. It does not mean "the file is safe": environment, libraries, the interpreter's version and anything the script fetches at run time are yours, and only the first entrypoint is verified. Composition (pipes, redirection, `&&`) goes inside the script, where it is reviewed and hashed with it. A reviewer can mark an approval standing, and an unchanged re-run then stops asking anyone; one changed byte re-queues review. `--reuse-days N` proposes how long that standing approval should last, 1 to 366 days. A proposal is not a grant: the approver decides whether reuse is granted at all, and a workspace may set a ceiling lower than N, in which case the server refuses the request with `file_exec_reuse_exceeds_max` rather than shortening it—resubmit with a shorter duration or none. A grant always expires, after the proposed days or at the workspace ceiling (1 to 366 days, default 90). Without the flag the request carries no proposal; the approver may still grant reuse up to the ceiling. A deployment with the command assessor disabled refuses the lane with guidance to run the script as an ordinary command instead. So does a target whose agent does not offer the file lane: an Alpamon older than 2.8.3, which updating to 2.8.3 or newer fixes, or a platform without the file lane, such as Windows, which updating does not fix. In both cases the guidance is to run the script as an ordinary command with an interpreter the server has.

### File transfer
```bash
$ alpacon cp ./local.txt <server>:/home/user/
$ alpacon cp <server>:/home/user/file.txt .
$ alpacon cp -u admin -g developers <SOURCE> <DESTINATION>
$ alpacon edit <server>:/etc/nginx/nginx.conf    # open a remote file in your local editor
```

`<server>:<path>` denotes a remote target. A file `cp` downloads is created owner-only—`0600` before the umask, which can only narrow it further—since remote files routinely carry secrets. A local file that already exists keeps its current mode instead; for such a single-file download, a warning on stderr says so when that kept mode is group- or other-readable. Recursive downloads and downloads of two or more sources arrive as an archive whose entries carry no Unix mode, so each extracted file lands at `0666` before the umask whatever its mode was on the server, and each directory created along the way at `0777` before the umask. A local file the archive overwrites keeps its own mode there too, and no warning covers that path. Saving in `edit` overwrites the remote file; ownership and permissions may be reset by server policy. `edit` only opens existing remote files—it downloads first, so it won't create a new one. `--editor` is tokenized without a shell (the file path is appended as the last argument), so shell syntax such as pipes (`|`), redirections (`>>`), or `&&` won't work.

`alpacon cp` requires Alpacon Server 2.36.0 or later, both for Alpacon Cloud workspaces and self-hosted workspaces. Below that version, it fails on its first transfer-status poll instead of waiting for the transfer to finish.

### TCP tunneling
```bash
$ alpacon tunnel <server> -l 9000 -r 8082
$ alpacon tunnel prod-db -l 5432 -r 5432 -- psql -h 127.0.0.1 -p 5432 -U app appdb
$ alpacon tunnel prod-k8s -l 6443 -r 6443 -- kubectl --server=https://127.0.0.1:6443 get pods
```

`--` separates the tunnel command from the inner command. `alpacon tunnel` does not auto-detect app ports—pass `127.0.0.1:<LOCAL_PORT>` explicitly.

### Work sessions
```bash
$ alpacon work-session ls                          # my active sessions (default)
$ alpacon work-session ls --status all             # my sessions in any status
$ alpacon work-session ls --user all               # everyone's active sessions
$ alpacon work-session ls --user all --status all  # all sessions
$ alpacon work-session current
$ alpacon work-session use <session-id>          # set active session
$ alpacon work-session use --unset
$ alpacon work-session extend <session-id> --expires-in 2h --reason "customer escalation, still triaging"
$ alpacon work-session revoke <session-id>       # superuser
$ alpacon work-session cancel <session-id>       # requester withdraws own pending request
# Approving/rejecting a session happens in the Alpacon console (web), not the CLI.
```

Override the active session per command with `--work-session <id>` or `ALPACON_WORK_SESSION=<id>`. Resolution order: `--work-session` flag > env var > active session.

`complete` and `revoke` clear the active session when they end it. A session that ends anywhere else—it expires, an admin revokes it, or it is completed from the console—stays set until you run `alpacon work-session use --unset`; `ls` stops marking it as active and `current` warns that it has ended.

`create --account <name>` names the account the work runs as on the servers (for example `root`), so the approver sees it with the request. The session runs only as that account, so pass it to later commands with `-u <name>` (for example `alpacon exec -u root web-01 -- uptime`); without it `exec` is refused with `work_session_account_not_approved`.

#### AI agents

The CLI detects when an AI coding agent runs it, from environment variables only, and names the agent on every request with the `X-Alpacon-Declared-Agent-Name` header (and `X-Alpacon-Declared-Agent-Version` when the environment states a version). The first match names the agent:

| Variable | Agent |
|---|---|
| `AI_AGENT=<name>` | the name given |
| `CLAUDE_CODE_CHILD_SESSION=1` | `claude-code` |
| `CODEX_CI=1`, `CODEX_THREAD_ID`, or `CODEX_SANDBOX` | `codex` |
| `GEMINI_CLI=1` | `gemini-cli` |
| `COPILOT_AGENT=1` or `COPILOT_CLI=1` | `github-copilot` |
| `CURSOR_AGENT=1` | `cursor` |
| `CLINE_ACTIVE=true` | `cline` |
| `GOOSE_TERMINAL=1` or `AGENT=goose` | `goose` |

Commands run through Claude Code's `!` prefix count as an agent too.

None of these count: no TTY, `CI`, `TERM_PROGRAM` or any other editor terminal, `CLAUDECODE` on its own, `REPL_ID`, `GOOSE_PROVIDER`, and `AGENT` with any value but `goose`. For an agent that sets none of the variables (for example Aider), set `AI_AGENT=<name>` in its environment.

When an agent is detected, `work-session create` opens an agent session. `--requester-type agent` opens one without naming an agent, and `--requester-type user` is refused with exit code `2` and a message naming every variable found—unset them, or run the command from your own terminal.

`extend` reads the session's `expires_at` back from the server rather than echoing the `--expires-in`/`--expires-at` value it sent, so the printed result is always what was actually applied.

`--reason` is required on every `extend`—a short justification an approver judges the request by. Whether the extension applies immediately or waits for approval follows the workspace's approval policy, the same rule `work-session create` uses: the CLI reports a queued extension pending (`status` `pending_approval` under `--output json`, with `requested_expires_at` and the request's own deadline in `context`; exit `4`) instead of the session already extended. Check `alpacon work-session describe <id>` for the request's status (`--output json` shows it under `pending_extension_request`), then run `extend` again with a new reason once it settles—`extend` does not wait on its own. `error_code` `work_session_extension_reason_required` means `--reason` was missing or blank; `work_session_extension_already_pending` means a decision is already outstanding for this session (find its id with `alpacon work-session describe <id> --output json`)—wait for it before asking again.

### Identity (users, groups)
```bash
$ alpacon user ls
$ alpacon user describe <username>
$ alpacon user create / update / rm
$ alpacon group ls
$ alpacon group member add --group <group> -u <user> --role <role>
$ alpacon group member rm --group <group> -u <user>
```

### Workspace roles
RBAC roles are the single source of truth for what an account is and may do. Granting `admin` is what makes someone a workspace administrator, and granting `superuser` is what makes someone a platform operator—the `is_staff` and `is_superuser` fields on a user are read-only projections of those roles, so editing them in `alpacon user update` does nothing and the command says so.

```bash
$ alpacon user role ls <username>                   # roles a user holds, and at what scope
$ alpacon user role catalog                         # roles this workspace defines
$ alpacon user role describe <role>                 # what it grants, and who holds it
$ alpacon user role grant  <username> <role> --reason "<why>"
$ alpacon user role revoke <username> superuser --cascade
$ alpacon user role history <username>              # who changed what, and why
$ alpacon user permission ls <username>             # what those roles let them do
$ alpacon user permission can-i <username> server:update
```

Granting `superuser` also creates a companion `admin` binding, so revoking `superuser` demotes to admin and leaves that companion in place; `--cascade` removes both. Changing a binding requires a workspace superuser and recent MFA.

On Alpacon Cloud workspaces an API token is refused on the `alpacon user role` commands, reads included, so they need a browser login. Two carve-outs: `alpacon user role history` reads the audit log, which accepts a token carrying the `role_audit_log:read` scope on either deployment; and `alpacon user permission` is hosted on the IAM user endpoint, which accepts a token on either deployment—except `can-i --explain`, which posts to the RBAC troubleshooter. On self-hosted workspaces a token works throughout and skips the MFA step.

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
$ alpacon agent restart <server>
$ alpacon agent upgrade <server>
$ alpacon agent install-pam <server>             # install alpamon-pam through the agent
$ alpacon server refresh <server>                # re-collect system information
```

`agent restart` and `agent upgrade` ask for confirmation first; pass `-y` to skip the prompt, which a non-interactive run (a script or CI job) needs. If the server has active user work—an open Websh/WebFTP session or an in-flight command—they are refused with exit code `5`; retry when idle, or pass `--force` to override. When the workspace asks for MFA, the CLI walks you through it and retries the request.

`agent install-pam` installs the `alpamon-pam` package on the server as root through the host's package manager, and leaves a server that already has the current version as is. It asks for confirmation and takes `-y` the same way, but it is not disruptive: active user work does not refuse it, and it has no `--force`. The server refuses it with `server_pam_install_unsupported` on a host with no `alpamon-pam` package (macOS, Windows, or an unknown package manager), with `server_pam_install_requires_sudo_with_mfa` while the workspace's "Use sudo with MFA" setting is off or the deployment cannot complete the MFA step-up (a self-hosted site without Auth0), and with `server_pam_install_in_progress` while an install is already pending on that server.

The CLI does not reboot, shut down, or upgrade the operating system of a server. Run those inside a work session instead, where sudo, MFA, policy or approval, and recording apply:

```bash
$ alpacon exec <server> -- sudo reboot
```

### Logs and audit
```bash
$ alpacon log <server> --tail=10
$ alpacon audit <filters>                        # workspace audit log
```

### More commands

Run `alpacon --help` for the full list, or `alpacon <command> --help` for details on any command.

## When a command is denied

Under interactive auth (browser login), `websh`, `exec`, `cp`, `edit`, and `tunnel` require an active work session. Without one, the command is refused with a diagnostic and exit code `3`:

```
Error: the command operation requires an active WorkSession on this authentication.

  auth          : Browser login (interactive)
  reason        : no WorkSession selected for this shell
  required scope: command
  target server : prod-1

Next:
  alpacon work-session ls --status active  # find an existing active session; AI agent: reuse it by prefixing the gated command with --work-session <ID>
  alpacon work-session use <ID>  # human: attach an existing session (rejects agent sessions)
  alpacon work-session create --scope command --server prod-1 --expires-in 1h --purpose "<intent>" --use  # none active? create a new one (human)
  alpacon work-session create --scope command --server prod-1 --expires-in 1h --purpose "<intent>" --requester-type agent  # none active? create a new one (AI agent; prefix the gated command with --work-session <ID>)

Note: Tokens issued by Alpacon (service or personal API token) bypass this check.
```

With `--output json`, the same refusal is a structured envelope on stderr—scripts and AI agents branch on `error_code` and exec each `next_actions[].command` directly (the human hint, when present, is a separate `description` field):

```json
{
  "ok": false,
  "exit_code": 3,
  "error_code": "work_session_required",
  "message": "the command operation requires an active WorkSession on this authentication.",
  "reason": "no WorkSession selected for this shell",
  "context": {
    "auth_method": "Browser login",
    "required_scope": "command",
    "target_servers": ["prod-1"],
    "current_worksession": null
  },
  "next_actions": [
    {"command": "alpacon work-session ls --status active", "description": "find an existing active session; AI agent: reuse it by prefixing the gated command with --work-session <ID>"},
    {"command": "alpacon work-session use <ID>", "description": "human: attach an existing session (rejects agent sessions)"},
    {"command": "alpacon work-session create --scope command --server prod-1 --expires-in 1h --purpose \"<intent>\" --use", "description": "none active? create a new one (human)"},
    {"command": "alpacon work-session create --scope command --server prod-1 --expires-in 1h --purpose \"<intent>\" --requester-type agent", "description": "none active? create a new one (AI agent; prefix the gated command with --work-session <ID>)"}
  ]
}
```

What each refusal code means and what to do next:

| `error_code` | Meaning | Next |
|---|---|---|
| `work_session_required` | no session selected for this shell | `work-session create --use` or `work-session use <ID>` |
| `work_session_not_active` | session not active (pending, approved, completed, revoked, or cancelled) | if pending or approved, wait; otherwise create or reuse a session |
| `work_session_expired` | session has expired | `work-session extend <ID>` or create a new one |
| `work_session_scope_not_allowed` | operation not in session scopes | create a session with the right `--scope` |
| `work_session_server_not_allowed` | target server not in session | create a session with the right `--server` |
| `work_session_assignee_mismatch` | session assigned to another principal | `work-session use <ID>` with your own session |
| `work_session_agent_session_required` | the CLI runs under an AI agent, and the session is a user session | create an agent session and pass its ID with `--work-session`; not an AI agent? unset the variables the diagnostic names, or run from your own terminal |
| `work_session_not_usable` | session is no longer usable | `work-session create --use` |

`work-session` subcommand failures (`create`, `use`, `extend`, ...), `event wait` / `event watch` failures, and the inline-credential refusal below also emit a JSON error envelope under `--output json`, with `error_code` carrying the server code when available, and `exit_code` matching the process exit code—`1` for a failed request, `2` with `error_code` `usage_error` for a flag or argument the command rejected. These envelopes may share an `error_code` with the gate-denial envelopes above—distinguish a subcommand failure (`exit_code: 1` or `2`) from a gate denial (`exit_code: 3`) via `exit_code`, not `error_code` alone. Run `alpacon whoami` to check upfront whether a work session is required for your auth.

Separately from the work session gate, `exec` (and `websh` when running a command) is refused before the command runs if the command line itself carries a credential—a `-p`/`--password` flag, a `KEY=VALUE` secret such as `PGPASSWORD=...`, or a `user:pass@host` connection string. Pass the secret with `--env="KEY"` instead: its value is read from your shell, so it never lands on the command line the server stores. A script run with `--file` gets the same refusal when the script carries the credential, but `--env` does not combine with `--file`: take the secret out of the script and have the script read it from a file or the environment on the host at run time. The refusal is permanent—a retry submits the same command line or script—so rewrite rather than retry.

Exit code is `1`, and under `--output json` the refusal is an error envelope on stderr with `error_code` `command_inline_credential`. That envelope currently carries no `next_actions`—rewrite the command with `--env="KEY"` as described above (`exec` takes the remote command after `--`, `websh` takes it as one quoted argument), or, for a `--file` run, rewrite the script to read the secret on the host.

Also separate from the work session gate: when you authenticate with an API token or a service token, the server checks the request against that token's ACL rules before anything runs. A request outside those rules is refused with exit code `1` and a message naming token access control. This is a permanent refusal, not a transient one: a retry submits the same request. List the rules with `alpacon token acl command ls TOKEN`, `alpacon token acl server ls TOKEN`, or `alpacon token acl file ls TOKEN`, and widen them with the matching `add` subcommand. Deny-by-default applies per ACL type—a token with no rule of a given type has no access of that kind at all.

## Exit codes

| Code | Meaning |
|------|---------|
| `0`  | Success |
| `1`  | General error (network failure, server error, etc.), and permanent refusals such as a credential on the command line (`command_inline_credential`)—see "When a command is denied". Also: `alpacon user update` when the edit touched `is_staff` or `is_superuser`, which are read-only and are never sent (any other field in the same edit is still applied); and a negative answer from `alpacon user permission can-i -q`, which is indistinguishable from a failed check by exit code alone—drop `-q` and read the `allowed` field of `--output json` when a script has to tell them apart. Also: a list cut short by a failed read—see "Partial list output" below |
| `2`  | Usage error—a flag or argument rejected by a `work-session` subcommand, by `event wait` / `event watch`, by `alpacon api`'s own input validation, or by the positive-value check behind `--tail`, `--limit`, and `--valid-days` (`utils.ExitCodeUsageError`). Under `--output json` the `work-session` and `event` paths emit an error envelope carrying `error_code` `usage_error`; `alpacon api` and the positive-value check print a plain message to stderr. Other commands still exit `1` when they reject a flag or argument, as do errors Cobra itself rejects while parsing (an unknown flag or subcommand) |
| `3`  | WorkSession gate denied—the active session does not authorize this action |
| `4`  | Pending human approval—the action is awaiting an out-of-band approve/reject in the Alpacon console (web/Slack), not refused. For `exec`, re-run the command after approval (or pass `--wait` on the original command to block; `--wait-approval <duration>` raises the wait timeout, default 5m); `websh` command mode has no `--wait`, so re-run via `alpacon exec --wait` to block; for `work-session create` the session already exists—after approval attach it with `alpacon work-session use <id>` (or pass `--wait` on the original create to block; `--wait-approval <duration>` raises the wait timeout, default 5m); for `work-session extend`, the session's current expiry is unchanged until the request is decided—check `alpacon work-session describe <id>` and re-run `extend` with a new reason once it settles (`extend` has no `--wait`: the pending request is visible only to its approvers, not to the requester who filed it). Under `--output json`, a `{"status":"pending_approval", ...}` object is emitted. Returned by `exec` (and `websh` when running a command) on a sudo denial that created an approval request (`SUDO_APPROVAL_REQUIRED`, or `SUDO_INTENT_DEVIATION` when the command reads as off-purpose for the work session)—including when `exec --wait` ends with the request still open, whether the window elapsed or the CLI could not reach the server for a bounded run of polls—and by `work-session create` when the session lands pending or when its `--wait` ends with the outcome still open, whether the window elapsed or the CLI could not reach the server for a bounded run of polls. Also returned by `work-session extend` when the request is not auto-approved; by `alpacon event wait` when the wait times out or is interrupted; and by `alpacon exec logs` on a job still held for approval—the outcome is still open in all of these |
| `5`  | Server busy with active user work—`alpacon agent restart` or `alpacon agent upgrade` was refused because the server has an open Websh/WebFTP session or in-flight command. Transient and retryable: retry when idle, or re-run with `--force` to override |
| `6`  | Approval not granted—an awaited approval settled without being granted (rejected, expired, revoked, cancelled, or completed). Distinct from `4`: the outcome is final, so retrying the same request only generates another approval request. Returned by `alpacon event wait`, by `work-session create --wait`, and—when the command's own approval was refused or its window lapsed—by `exec`, by `websh` command mode, and by `alpacon exec logs`. Under `--output json` these paths emit an error envelope carrying the same `exit_code`. For `work-session create --wait`, the session already exists and outlives the refusal, so the CLI names `work-session describe <id>` and `work-session complete <id>` as next steps—not `work-session use <id>`, which only attaches an active session and a settled one can never become active again—on stderr as plain text, and under `--output json` both as `next_actions` and as `work_session_id` in the envelope's `context` |
| `7`  | Purpose required—the verification gate held an agent's command and is asking what it is for, before any approval request exists. Distinct from `4`: nothing is pending on a human, nobody has been notified, and the next move belongs to the caller that submitted the command. Answer with `alpacon exec purpose <JOB_ID> "..."` within about a minute; `--wait` does not apply, because the answer is yours to give rather than somebody else's to grant. Pass `--purpose` on the original `exec` to state it up front and skip the demand entirely. One demand per command: on silence the command takes the ordinary path, and a late or second answer is refused. Under `--output json`, a `{"status":"purpose_required", ...}` object is emitted carrying the command id, the seconds left (when the server reports the demand's expiry), and what makes a purpose useful. `alpacon exec purpose` answers on the same contract, emitting `purpose_recorded` on success and `purpose_refused` (exit `1`) when the demand is already settled or the credential is not the one that submitted the command. Returned by `exec`, by `websh` command mode (which reaches the same handler through `RunRemoteExec` but has no `--purpose`, so a demand there can only be answered with `alpacon exec purpose`), and by `alpacon exec logs`—the only sight `--detach` has of a demand, and worth knowing because a held command sits for the length of the window before taking the ordinary path |
| `8`  | A newer release is available—returned only by `alpacon update --check`, which reports the newer version and installs nothing. Distinct from `1`: `1` means the check itself failed, `8` means it succeeded and found a newer version. The check prints what to run: `alpacon update` for a binary you placed yourself, and for one another tool owns, how to update through that tool |

### Partial list output

When a paginated read fails part-way, `log`, `audit`, `webftp-log`, and `websh records` print the entries collected before the failure rather than nothing. Stdout carries the short list and stderr says how short it is; under `--output json` the list is a plain array with nothing in it marking it as partial. The exit code is `1`, so a script reading their output has to check the exit code before taking the list for the whole answer.

## Contributing

```bash
git clone https://github.com/alpacax/alpacon-cli.git
cd alpacon-cli
go build
go test -count=1 ./...
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
