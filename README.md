# Alpacon CLI

[![Go Version](https://img.shields.io/github/go-mod/go-version/alpacax/alpacon-cli)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](https://github.com/alpacax/alpacon-cli/blob/main/LICENSE)
[![Latest Release](https://img.shields.io/github/v/release/alpacax/alpacon-cli)](https://github.com/alpacax/alpacon-cli/releases)

`Alpacon CLI` is a command-line tool for [Alpacon](https://alpacon.io) — a zero-trust infrastructure access platform that replaces SSH keys, VPNs, and bastion hosts with a single secure identity. Alpacon enables teams to scale operations across servers and customer environments without managing per-server credentials, and provides API tokens for CI/CD pipelines and AI agents to access infrastructure safely.

This CLI lets you interact with your Alpacon workspace directly from the terminal: open browser-based terminals, execute remote commands, transfer files, create TCP tunnels, and manage certificates — all with built-in MFA, session recording, and role-based access controls.

## Architecture

Alpacon consists of the following components:

- **Alpacon Server** — The zero-trust access platform with encrypted connections, MFA, session recording, and granular access controls. No inbound ports required. Sign up at [alpacon.io](https://alpacon.io).
- **[Alpamon](https://github.com/alpacax/alpamon)** — An open-source agent installed on managed servers to enable remote access and monitoring.
- **Alpacon CLI** (this repository) — A command-line client for interacting with your Alpacon workspace. Also provides API tokens for CI/CD and automation.

## Documentation

> [!NOTE]
> This README is intended as a **development and contribution guide**.
> For production usage and deployment, please refer to the [official documentation](https://docs.alpacax.com/reference/cli/).

## Installation

Download the latest `Alpacon CLI` directly from our [releases page](https://github.com/alpacax/alpacon-cli/releases) or install it using package managers.

> [!IMPORTANT]
> Building from source is intended for **development purposes only**.
> For production use, please install via package managers or download pre-built binaries from the [releases page](https://github.com/alpacax/alpacon-cli/releases).
> See the [official documentation](https://docs.alpacax.com/reference/cli/) for more details.

### Docker

For every release and Release Candidate (RC), we push a corresponding container image to our Docker Hub repository at `alpacax/alpacon-cli`. For example:

```bash
docker run --rm -it alpacax/alpacon-cli version
```

### Build from source

Make sure you have [Go](https://go.dev/dl/) installed:

```bash
git clone https://github.com/alpacax/alpacon-cli.git
cd alpacon-cli
go build
sudo mv alpacon-cli /usr/local/bin/alpacon
```

### macOS

#### Homebrew
```bash
brew tap alpacax/alpacon
brew install alpacon-cli
```

> **Note for existing users**: If you encounter any issues with `brew upgrade`, please run:
> ```bash
> brew uninstall alpacon-cli
> brew untap alpacax/cli
> brew tap alpacax/alpacon
> brew install alpacon-cli
> ```

#### Download from GitHub releases
```bash
VERSION=<latest-version> # Replace with the actual version
wget https://github.com/alpacax/alpacon-cli/releases/download/${VERSION}/alpacon-${VERSION}-darwin-arm64.tar.gz
tar -xvf alpacon-${VERSION}-darwin-arm64.tar.gz
chmod +x alpacon
sudo mv alpacon /usr/local/bin
```


### Linux

#### Debian and Ubuntu
```bash
curl -s https://packagecloud.io/install/repositories/alpacax/alpacon/script.deb.sh?any=true | sudo bash

sudo apt-get install alpacon
```

#### CentOS and RHEL
```bash
curl -s https://packagecloud.io/install/repositories/alpacax/alpacon/script.rpm.sh?any=true | sudo bash

sudo yum install alpacon
```

#### Download from GitHub releases
```bash
VERSION=<latest-version> # Replace with the actual version
wget https://github.com/alpacax/alpacon-cli/releases/download/${VERSION}/alpacon-${VERSION}-linux-amd64.tar.gz
tar -xvf alpacon-${VERSION}-linux-amd64.tar.gz
chmod +x alpacon
sudo mv alpacon /usr/local/bin
```

### Windows

Download the latest `.zip` archive for Windows from [GitHub Releases](https://github.com/alpacax/alpacon-cli/releases) and add the binary to your PATH.


### Login & logout
To access and utilize all features of `Alpacon CLI`, first authenticate with your Alpacon workspace:

```bash
$ alpacon login

# Cloud login (portal URL or API URL)
$ alpacon login https://alpacon.io/myworkspace
$ alpacon login myworkspace.us1.alpacon.io

# Self-hosted
$ alpacon login alpacon.example.com

# Log in via API token
$ alpacon login myworkspace.us1.alpacon.io -t [TOKEN_KEY]

# Legacy username/password
$ alpacon login [WORKSPACE_URL] -u [USERNAME] -p [PASSWORD]

# Skip TLS certificate verification
$ alpacon login [WORKSPACE_URL] --insecure

# Disable auto-open browser (useful for CI, scripted, or remote sessions)
$ alpacon login --no-browser
$ ALPACON_NO_BROWSER=1 alpacon login

# Logout
$ alpacon logout
```

For Auth0 and MFA authentication, the CLI automatically opens the authentication URL in your default browser. This is skipped in SSH sessions and headless environments. To disable it explicitly, use `--no-browser` or set `ALPACON_NO_BROWSER=1`. The `ALPACON_NO_BROWSER` environment variable also applies to MFA prompts triggered by other commands.

A successful login generates a `config.json` file in `~/.alpacon`, which includes the workspace url, API token, and token expiration time (approximately 1 week).
This file is crucial for executing commands, and you will need to log in again once the token expires.

Upon re-login, the Alpacon CLI will automatically reuse the workspace URL from `config.json`, unless you provide a workspace URL as an argument.

## Usage
Explore Alpacon CLI's capabilities with the `-h` or `help` command.

```bash
$ alpacon -h

Use this tool to interact with the alpacon service.

Usage:
  alpacon [flags]
  alpacon [command]

Available Commands:
  agent       Commands to manage server's agent
  authority   Commands to manage and interact with certificate authorities
  cert        Manage and interact with SSL/TLS certificates
  completion  Generate the autocompletion script for the specified shell
  cp          Copy files between local and remote locations
  csr         Generate and manage Certificate Signing Request (CSR) operations
  event       Retrieve and display recent Alpacon events.
  exec        Execute a command on a remote server
  group       Manage Group resources
  help        Help about any command
  log         Retrieve and display server logs
  login       Log in to Alpacon
  logout      Log out of Alpacon
  note        Manage and view server notes
  package     Commands to manage and interact with packages
  server      Commands to manage and interact with servers
  token       Commands to manage api tokens
  tunnel      Create a TCP tunnel to a remote server
  user        Manage User resources
  version     Displays the current CLI version.
  websh       Open a websh terminal or execute a command on a server
```
### Examples of use cases

#### Server management
Manage and interact with servers efficiently using Alpacon CLI:
```bash
# List all servers.
$ alpacon server ls / list / all

# Get detailed information about a specific server.
$ alpacon server describe [SERVER NAME]

# Interactive server creation process.
$ alpacon server create

# Delete server
$ alpacon server delete [SERVER NAME]
$ alpacon server rm [SERVER NAME]

Server Name:
Platform(debian, rhel):
Groups:
[1] alpacon
[2] auditors
[3] designers
[4] developers
[5] managers
[6] operators
Select groups that are authorized to access this server. (e.g., 1,2):
```

#### Connect websh
Access a server's websh terminal:
```bash
# Open a websh terminal
$ alpacon websh my-server

# Open as root using SSH-like syntax
$ alpacon websh root@my-server

# Open with specific user and group
$ alpacon websh -u admin -g developers my-server
```

#### Execute a command via websh
Execute a command directly on a server and retrieve the output:
```bash
$ alpacon websh my-server "ls -la /var/log"

$ alpacon websh root@my-server "systemctl status nginx"

$ alpacon websh -u admin -g developers my-server "docker ps"

$ alpacon websh --env="KEY1=VALUE1" --env="KEY2=VALUE2" my-server "echo $KEY1"
```
> **Note**: All flags must be placed before the server name. Everything after the server name is treated as the remote command.

#### Execute a command (SSH-style)
Execute a command on a remote server using SSH-like `user@host` syntax:
```bash
# Execute a command on a server
$ alpacon exec [SERVER NAME] [COMMAND]

# Use SSH-style user@host syntax
$ alpacon exec root@prod-docker docker ps
$ alpacon exec admin@web-server ls -la /var/log

# Specify username and groupname via flags
$ alpacon exec -u root prod-docker systemctl status nginx
$ alpacon exec -g docker user@server docker images
```

#### TCP tunnel
Create a TCP tunnel that forwards local port traffic to a remote server's port:
```bash
# Forward local port 9000 to remote server's port 8082
$ alpacon tunnel my-server -l 9000 -r 8082

# Forward local port 2222 to remote server's SSH port (22)
$ alpacon tunnel my-server -l 2222 -r 22

# Specify username and groupname for the tunnel
$ alpacon tunnel my-server -l 9000 -r 8082 -u admin -g developers

# Enable verbose connection logs
$ alpacon tunnel my-server -l 9000 -r 8082 -v
```

Run a local TCP application in the same session with an attached tunnel:
```bash
# psql through tunnel (local 5432 -> remote 5432)
$ alpacon tunnel prod-db -l 5432 -r 5432 -- psql -h 127.0.0.1 -p 5432 -U app appdb

# kubectl through tunnel (local 6443 -> remote 6443)
$ alpacon tunnel prod-k8s -l 6443 -r 6443 -- kubectl --server=https://127.0.0.1:6443 get pods

# SSH through tunnel
$ alpacon tunnel my-server -l 2222 -r 22 -- ssh -p 2222 user@127.0.0.1
```
> `--` separator is required.  
> `alpacon tunnel` does not auto-detect app ports; pass the app's local target (e.g. `127.0.0.1:<LOCAL_PORT>`) explicitly.
> Prefer `-- COMMAND [ARGS...]` over a single quoted command string.  
> If you really need shell one-liner style, use `-- sh -c "..."`.


#### Share your websh terminal
You can share the current terminal to others via a temporary link:
```bash
# Open a websh terminal and share the current terminal
$ alpacon websh --share my-server
$ alpacon websh --share --read-only=true my-server

# Join an existing shared session
$ alpacon websh join --url [SHARED_URL] --password [PASSWORD]
```



#### Identity and access management (IAM)
Efficiently manage user and group resources:
```bash
# Managing users

# List all users.
$ alpacon user ls / list / all

# Detailed user information.
$ alpacon user describe [USER NAME]

# Create a new user
$ alpacon user create

# Update the user information.
$ alpacon user update [USER NAME]

# Delete user
$ alpacon user delete [USER NAME]
$ alpacon user rm [USER NAME]

# Managing groups

# List all groups.
$ alpacon group ls

# Detailed group information.
$ alpacon group describe [GROUP NAME]

# Delete group
$ alpacon group delete [GROUP NAME]
$ alpacon group rm [GROUP NAME]

# Add a member to a group with a specific role
$ alpacon group member add
$ alpacon group member add --group [GROUP NAME] --member [MEMBER NAME] --role [ROLE]

# Remove a member from a group
$ alpacon group member delete --group [GROUP NAME] --member[MEMBER NAME]
$ alpacon group member rm --group [GROUP NAME] --member [MEMBER NAME]
```

#### API tokens
API tokens can be used to access alpacon.
```bash
# Create a new API token
$ alpacon token create
$ alpacon token create -n [TOKEN NAME] -l / --limit=true
$ alpacon token create -n [TOKEN NAME] --expiration-in-days=7

# Display a list of API tokens in the Alpacon
$ alpacon token ls

# Delete API token
$ alpacon token delete [TOKEN_ID_OR_NAME]
$ alpacon token rm [TOKEN_ID_OR_NAME]

# Log in via API token
$ alpacon login -s [SERVER URL] -t [TOKEN KEY]
```

#### Command ACL in API token
Defines command access for API tokens and enables setting specific commands that each API token can run.
```bash
# Add a new command ACL with specific token and command.
$ alpacon token acl add [TOKEN_ID_OR_NAME]
$ alpacon token acl add --token=[TOKEN_ID_OR_NAME] --command=[COMMAND]

# Display all command ACLs for an API token.
$ alpacon token acl ls [TOKEN_ID_OR_NAME]

# Delete the specified command ACL from an API token.
$ alpacon token acl delete [COMMAND_ACL_ID]
$ alpacon token acl rm [COMMAND_ACL_ID]
$ alpacon token acl delete --token=[TOKEN_ID_OR_NAME] --command=[COMMAND]
```

#### File transfer
Facilitate file uploads and downloads:
```bash
$ alpacon cp [SOURCE] [DESTINATION]

# Upload files
$ alpacon cp /Users/alpacon.txt myserver:/home/alpacon/

# Download files
$ alpacon cp myserver:/home/alpacon/alpacon.txt .

# To use a specified username and groupname for the transfer:
$ alpacon cp -u [USER NAME] -g [GROUP NAME] [SOURCE] [DESTINATION]
```
- `[SERVER NAME]:[PATH]` : denotes the server's name and the file's path for FTP operations.

#### Package management
Handle Python and system packages effortlessly:
```bash
# python
$ alpacon package python ls / list / all
$ alpacon package python upload alpamon-1.1.0-py3-none-any.whl
$ alpacon package python download alpamon-1.1.0-py3-none-any.whl .

# system
$ alpacon package system ls / list / all
$ alpacon package system upload osquery-5.10.2-1.linux.x86_64.rpm
$ alpacon package system download osquery-5.10.2-1.linux.x86_64.rpm .
```

#### Logs management
Retrieve and monitor server logs:
```bash
# View recent logs or tail specific logs.
$ alpacon logs [SERVER_NAME]
$ alpacon logs [SERVER NAME] --tail=10
```

#### Events management
Retrieve and monitor events in the Alpacon:
```bash
# Display a list of recent events in the Alpacon
$ alpacon event
$ alpacon events

# Tail the last 10 events related to a specific server and requested by a specific user
$ alpacon event -t 10 -s myserver -u admin
$ alpacon event --tail=10 --server=myserver --user=admin
```

#### Agent (Alpamon) commands
Manage server agents(Alpamon) with ease:
```bash
# Commands to control and upgrade server agents.
$ alpacon agent restart [SERVER NAME]
$ alpacon agent upgrade [SERVER NAME]
$ alpacon agent shutdown [SERVER NAME]
```

#### Note commands
Manage and view server notes:
```bash
# Display a list of all notes
$ alpacon note ls / list / all
$ alpacon note ls -s [SERVER NAME] --tail=10

# Create a note on the specified server
$ alpacon note create
$ alpacon note create -s [SERVER NAME] -c [CONTENT] -p [PRIVATE(true or false)]

# Delete a specified note
$ alpacon note delete [NOTE ID]
$ alpacon note rm [NOTE ID]
```

#### Private CA and certificate commands
Easily manage your private Certificate Authorities (CAs) and certificates:
```bash
# Create a new Certificate Authority
$ alpacon authority create

# List all Certificate Authorities
$ alpacon authority ls

# Get detailed information about a specific Certificate Authority.
$ alpacon authority describe [AUTHORITY ID]

# Download a root Certificate by authority's ID and save it to the specified file path.
$ alpacon authority download-crt [AUTHOIRY ID] --out=/path/to/root.crt

# Delete a CA along with its certificate and CSR
$ alpacon authority delete [AUTHORITY ID]
$ alpacon authority rm [AUTHORITY ID]

# Generate a new Certificate Signing Request (CSR)
$ alpacon csr create

# Display a list of CSRs, optionally filtered by status
$ alpacon csr ls
$ alpacon csr ls --status=signed

# Approve a Certificate Signing Request
$ alpacon csr approve [CSR ID]

# Deny a Certificate Signing Request
$ alpacon csr deny [CSR ID]

# Delete a Certificate Signing Request
$ alpacon csr delete [CSR ID]
$ alpacon csr rm [CSR ID]

# Get detailed information about a specific Signing Request.
$ alpacon csr describe [CSR ID]

# Download the signed certificate for a CSR
$ alpacon csr download-crt [CSR ID] --out=/path/to/certificate.crt

# List all certificates
$ alpacon cert ls

# Get detailed information about a specific Certificate.
$ alpacon cert describe [CERT ID]

# Download a specific Certificate by its ID and save it to the specified file path.
$ alpacon cert download [CERT ID] --out=/path/to/certificate.crt
```

### Test

To test the Alpacon CLI functionality, you can use the provided test script:

1. **Copy the sample test script:**
   ```bash
   cp sample_test_cli.sh test_cli.sh
   ```

2. **Edit the configuration variables to match your environment:**
   ```bash
   vi test_cli.sh
   ```

   Update the following variables in the Configuration section:
   ```bash
   # Configuration
   SERVER_NAME="your-server-name"           # e.g., "amazon-linux-1"
   LOCAL_PATH="/your/local/path"            # e.g., "/Users/username/Documents"
   REMOTE_ROOT_PATH="/root"                 # Usually "/root"
   REMOTE_USER_PATH="/your/remote/path"     # e.g., "/home/username"
   WORKSPACE_URL="your-workspace-url"       # e.g., "https://myworkspace.us1.alpacon.io"
   ```

3. **Make the script executable and run the tests:**
   ```bash
   chmod +x test_cli.sh
   ./test_cli.sh
   ```

The test script will automatically:
- Check if you're logged in to Alpacon (and login if necessary)
- Create test files and folders locally
- Run comprehensive tests covering:
  - Basic connectivity and server information
  - Command execution (regular and root user)
  - File upload/download operations
  - Folder upload/download operations (recursive)
  - websh functionality
  - Advanced operations and error handling
- Clean up all test files after completion

**Test Coverage:**
- 32 automated tests covering all major CLI functionality
- File and folder transfer operations
- Permission-based operations (user and root)
- SSH-style command syntax
- Error handling and edge cases

**Note:** Make sure you have access to the specified server and the necessary permissions for the test operations before running the script.

## Contributing

We welcome contributions! Here's how to get started:

### Build

```bash
git clone https://github.com/alpacax/alpacon-cli.git
cd alpacon-cli
go build
```

### Run tests

```bash
go test ./...
```

### Submitting changes

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/my-feature`)
3. Commit your changes
4. Push to the branch and open a Pull Request

Bug reports and feature requests are welcome on our [GitHub Issues](https://github.com/alpacax/alpacon-cli/issues) page.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
