package exec

import (
	"strings"

	"github.com/alpacax/alpacon-cli/utils"
)

// RemoteExecArgs holds parsed arguments for remote command execution.
type RemoteExecArgs struct {
	Username  string
	Groupname string
	Server    string
	Command   string
	ShowHelp  bool
	Err       string
}

// ParseRemoteExecArgs parses raw CLI arguments with manual flag handling.
// It recognizes -u/--username, -g/--groupname, and -h/--help flags before
// the server name. The -- separator stops flag parsing: everything after it
// is treated as the remote command. Without --, everything after the server
// name is the remote command.
//
// Layout: [flags] [USER@]SERVER [--] COMMAND...
func ParseRemoteExecArgs(args []string) RemoteExecArgs {
	var (
		username, groupname, server string
		commandParts               []string
	)

	for i := 0; i < len(args); i++ {
		arg := args[i]

		// -- separator: everything remaining is the remote command
		if arg == "--" {
			if server == "" {
				// Nothing before -- that looked like a server name.
				// Treat remaining args normally: first is server, rest is command.
				remaining := args[i+1:]
				if len(remaining) > 0 {
					server = remaining[0]
					commandParts = remaining[1:]
				}
			} else {
				commandParts = args[i+1:]
			}
			break
		}

		// Already found server: remaining args are the command
		if server != "" {
			commandParts = args[i:]
			break
		}

		// Flag parsing (only before server is identified)
		switch {
		case arg == "-h" || arg == "--help":
			return RemoteExecArgs{ShowHelp: true}
		case matchShortOrLongFlag(arg, "-u", "--username"):
			username, i = extractFlagValue(args, i, "-u")
		case matchShortOrLongFlag(arg, "-g", "--groupname"):
			groupname, i = extractFlagValue(args, i, "-g")
		case strings.HasPrefix(arg, "-"):
			return RemoteExecArgs{Err: "unknown flag: " + arg}
		default:
			server = arg
		}
	}

	// Parse SSH-like user@host syntax
	if server != "" && strings.Contains(server, "@") && !strings.Contains(server, ":") {
		sshTarget := utils.ParseSSHTarget(server)
		if username == "" && sshTarget.User != "" {
			username = sshTarget.User
		}
		server = sshTarget.Host
	}

	return RemoteExecArgs{
		Username:  username,
		Groupname: groupname,
		Server:    server,
		Command:   strings.Join(commandParts, " "),
	}
}

// matchShortOrLongFlag checks whether arg is an exact match for the given short
// or long flag name, or a prefixed form (-uVALUE, --username=VALUE).
func matchShortOrLongFlag(arg, short, long string) bool {
	return arg == short || strings.HasPrefix(arg, short+"=") || len(arg) > len(short) && strings.HasPrefix(arg, short) && arg[len(short)] != '-' ||
		arg == long || strings.HasPrefix(arg, long+"=")
}

// extractFlagValue extracts a flag value from one of three forms:
//   - attached short: -uroot          → "root"
//   - long with =:    --username=root → "root"
//   - separate:       -u root         → "root" (advances i)
func extractFlagValue(args []string, i int, short string) (string, int) {
	arg := args[i]
	// --flag=value
	if strings.Contains(arg, "=") {
		parts := strings.SplitN(arg, "=", 2)
		return parts[1], i
	}
	// -uroot (short flag with attached value, no space)
	if strings.HasPrefix(arg, short) && len(arg) > len(short) {
		return arg[len(short):], i
	}
	// -u root (next arg is the value)
	if i+1 < len(args) {
		return args[i+1], i + 1
	}
	return "", i
}
