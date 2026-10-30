package exec

import (
	"fmt"
	"os"
	pathpkg "path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/alpacax/alpacon-cli/utils"
)

// PurposeMaxLength is the server's ceiling on a stated purpose.
// Checked here so an over-long one is a usage error rather than a 400 that
// costs a parked command its one demand.
const PurposeMaxLength = 2000

// checkPurpose validates a stated purpose, returning the value to send and an empty message when it
// passes. subject names the flag or argument in the message, so `exec --purpose` and
// `exec purpose` share one ceiling and one wording.
//
// Surrounding whitespace is trimmed before the ceiling is counted, so the length checked is the
// length sent. Nothing else is stripped: removing control characters would falsify a statement the
// assessor and the audit record attribute to the requester.
//
// The ceiling is counted in runes because the server counts characters; len() would refuse a Korean
// purpose at roughly 666 of them.
func checkPurpose(subject, purpose string) (string, string) {
	trimmed := strings.TrimSpace(purpose)
	if trimmed == "" {
		return "", subject + " requires a value describing what this command is for"
	}
	if utf8.RuneCountInString(trimmed) > PurposeMaxLength {
		return "", fmt.Sprintf("%s is limited to %d characters; the server refuses a longer one", subject, PurposeMaxLength)
	}
	return trimmed, ""
}

// checkReuseDays returns the days to propose, or a message when raw is not a whole number in range.
func checkReuseDays(raw string) (int, string) {
	days, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Sprintf("--reuse-days requires a whole number of days (%d to %d): %s", reuseDaysMin, reuseDaysMax, raw)
	}
	if days < reuseDaysMin || days > reuseDaysMax {
		return 0, fmt.Sprintf("--reuse-days must be %d to %d days: %d", reuseDaysMin, reuseDaysMax, days)
	}
	return days, ""
}

// RemoteExecArgs holds parsed arguments for remote command execution.
type RemoteExecArgs struct {
	Username      string
	Groupname     string
	WorkSessionID string
	OutputFormat  string
	Server        string
	Command       string
	// What this command is for. Sent on submission, so the assessor judges
	// with it on the first pass and no demand is issued.
	Purpose string
	// InvokedAs selects which syntax a hint renders its example in. The caller
	// sets it, not ParseRemoteExecArgs: websh command mode marks its args
	// WebshInvocation, and exec leaves it empty, which the hint reads as exec.
	InvokedAs    Invocation
	Env          map[string]string
	UnsetEnv     []string // bare --env keys the shell does not set; see WarnUnsetEnv
	WaitApproval time.Duration
	Detach       bool
	Wait         bool
	ShowHelp     bool
	Err          string
	// File is set by --file and selects the verified file lane, in which case Command is empty and
	// the words after -- are the script's arguments. Nil on the generic lane.
	File *FileExecArgs
}

// ParseRemoteExecArgs parses raw CLI arguments with manual flag handling. Flags are recognized only
// before the server name, and all are read even on an error or -h, so OutputFormat holds the last --output.
//
// The -- separator has two roles depending on position:
//   - After the server name: everything after it is the remote command, with no further flag
//     parsing.
//   - Before the server name: it ends the flags; the first arg after it is the server, the rest
//     is the command.
//
// Without --, everything after the server name is the remote command.
//
// Layout: [flags] [USER@]SERVER [--] COMMAND...
//
// With --file the words after the server are the script's arguments and must follow --, so a
// command line is never mistaken for a verified file's arguments.
func ParseRemoteExecArgs(args []string) RemoteExecArgs {
	var (
		username, groupname, workSessionID, outputFormat, server string
		purpose                                                  string
		filePath, fileFrom, interpreter                          string
		reuseDays                                                int
		commandParts                                             []string
		detach                                                   bool
		upload, overwrite, fromServer                            bool
		wait                                                     bool
		separated                                                bool
		waitApproval                                             time.Duration
	)
	env := map[string]string{}
	var unsetEnv []string

	var stop RemoteExecArgs
	stopped := false
	halt := func(r RemoteExecArgs) {
		if !stopped {
			stop, stopped = r, true
		}
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if arg == "--" {
			separated = true
			if server == "" {
				// No server before --: the first arg after it is the server, the rest is the
				// command.
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

		if server != "" {
			commandParts = args[i:]
			break
		}

		switch {
		case arg == "-h" || arg == "--help":
			halt(RemoteExecArgs{ShowHelp: true})
		case matchShortOrLongFlag(arg, "-u", "--username"):
			var errMsg string
			username, i, errMsg = extractFlagValue(args, i, "-u")
			if errMsg != "" {
				halt(RemoteExecArgs{Err: errMsg})
				continue
			}
		case matchShortOrLongFlag(arg, "-g", "--groupname"):
			var errMsg string
			groupname, i, errMsg = extractFlagValue(args, i, "-g")
			if errMsg != "" {
				halt(RemoteExecArgs{Err: errMsg})
				continue
			}
		case arg == "--work-session" || strings.HasPrefix(arg, "--work-session="):
			var errMsg string
			// No short form: pass the long name as sentinel to disable attached-value parsing.
			workSessionID, i, errMsg = extractFlagValue(args, i, "--work-session")
			if errMsg != "" {
				halt(RemoteExecArgs{Err: errMsg})
				continue
			}
		case arg == "--purpose" || strings.HasPrefix(arg, "--purpose="):
			var errMsg string
			// No short form: pass the long name as sentinel to disable attached-value parsing.
			purpose, i, errMsg = extractFlagValue(args, i, "--purpose")
			if errMsg != "" {
				halt(RemoteExecArgs{Err: errMsg})
				continue
			}
			var msg string
			if purpose, msg = checkPurpose("--purpose", purpose); msg != "" {
				halt(RemoteExecArgs{Err: msg})
				continue
			}
		case arg == "--output" || strings.HasPrefix(arg, "--output="):
			var value, errMsg string
			value, i, errMsg = extractFlagValue(args, i, "--output")
			if errMsg != "" {
				halt(RemoteExecArgs{Err: errMsg})
				continue
			}
			if value == "" {
				halt(RemoteExecArgs{Err: "--output requires a value (table|json)"})
				continue
			}
			outputFormat = value
		case arg == "--env" || strings.HasPrefix(arg, "--env="):
			unset, errMsg := ParseEnvArg(arg, env)
			if errMsg != "" {
				halt(RemoteExecArgs{Err: errMsg})
				continue
			}
			if unset != "" {
				unsetEnv = append(unsetEnv, unset)
			}
		case arg == "--file" || strings.HasPrefix(arg, "--file="):
			var errMsg string
			filePath, i, errMsg = extractFlagValue(args, i, "--file")
			if errMsg != "" {
				halt(RemoteExecArgs{Err: errMsg})
				continue
			}
			if filePath == "" {
				halt(RemoteExecArgs{Err: "--file requires the script's absolute path on the target server"})
				continue
			}
			if !strings.HasPrefix(filePath, "/") {
				halt(RemoteExecArgs{Err: fmt.Sprintf("--file must be an absolute path on the target server (starting with /): %s", filePath)})
				continue
			}
		case arg == "--file-from" || strings.HasPrefix(arg, "--file-from="):
			var errMsg string
			fileFrom, i, errMsg = extractFlagValue(args, i, "--file-from")
			if errMsg != "" {
				halt(RemoteExecArgs{Err: errMsg})
				continue
			}
			if fileFrom == "" {
				halt(RemoteExecArgs{Err: "--file-from requires a local path to read the script from"})
				continue
			}
		case arg == "--interpreter" || strings.HasPrefix(arg, "--interpreter="):
			var errMsg string
			interpreter, i, errMsg = extractFlagValue(args, i, "--interpreter")
			if errMsg != "" {
				halt(RemoteExecArgs{Err: errMsg})
				continue
			}
			if interpreter == "" {
				halt(RemoteExecArgs{Err: "--interpreter requires the interpreter's absolute path on the target server"})
				continue
			}
			if !strings.HasPrefix(interpreter, "/") {
				halt(RemoteExecArgs{Err: fmt.Sprintf("--interpreter must be an absolute path (starting with /): %s", interpreter)})
				continue
			}
		case arg == "--reuse-days" || strings.HasPrefix(arg, "--reuse-days="):
			var raw, errMsg string
			raw, i, errMsg = extractFlagValue(args, i, "--reuse-days")
			if errMsg != "" {
				halt(RemoteExecArgs{Err: errMsg})
				continue
			}
			var msg string
			if reuseDays, msg = checkReuseDays(raw); msg != "" {
				halt(RemoteExecArgs{Err: msg})
				continue
			}
		case arg == "--upload":
			upload = true
		case strings.HasPrefix(arg, "--upload="):
			halt(RemoteExecArgs{Err: "--upload does not accept a value; use --upload alone"})
			continue
		case arg == "--overwrite":
			overwrite = true
		case strings.HasPrefix(arg, "--overwrite="):
			halt(RemoteExecArgs{Err: "--overwrite does not accept a value; use --overwrite alone"})
			continue
		case arg == "--from-server":
			fromServer = true
		case strings.HasPrefix(arg, "--from-server="):
			halt(RemoteExecArgs{Err: "--from-server does not accept a value; use --from-server alone"})
			continue
		case arg == "--detach":
			detach = true
		case strings.HasPrefix(arg, "--detach="):
			halt(RemoteExecArgs{Err: "--detach does not accept a value; use --detach alone"})
			continue
		case arg == "--wait":
			wait = true
		case strings.HasPrefix(arg, "--wait="):
			halt(RemoteExecArgs{Err: "--wait does not accept a value; use --wait alone"})
			continue
		case arg == "--wait-approval" || strings.HasPrefix(arg, "--wait-approval="):
			var raw, errMsg string
			raw, i, errMsg = extractFlagValue(args, i, "--wait-approval")
			if errMsg != "" {
				halt(RemoteExecArgs{Err: errMsg})
				continue
			}
			d, err := utils.ParsePositiveDuration("--wait-approval", raw)
			if err != nil {
				halt(RemoteExecArgs{Err: err.Error()})
				continue
			}
			waitApproval = d
		case strings.HasPrefix(arg, "-"):
			halt(RemoteExecArgs{Err: "unknown flag: " + arg})
			continue
		default:
			server = arg
		}
	}

	if detach && wait {
		halt(RemoteExecArgs{
			Err: "--wait and --detach cannot be combined; --detach returns immediately and would ignore --wait",
		})
	}
	if detach && waitApproval > 0 {
		halt(RemoteExecArgs{
			Err: "--wait-approval and --detach cannot be combined; --detach returns immediately and would ignore --wait-approval",
		})
	}
	if overwrite && !upload {
		halt(RemoteExecArgs{Err: "--overwrite requires --upload; it only applies to the file --upload writes"})
	}
	if filePath == "" {
		if fileFrom != "" {
			halt(RemoteExecArgs{Err: "--file-from requires --file"})
		}
		if interpreter != "" {
			halt(RemoteExecArgs{Err: "--interpreter requires --file"})
		}
		if reuseDays > 0 {
			halt(RemoteExecArgs{Err: "--reuse-days requires --file; only a verified file execution can propose a reuse duration"})
		}
		if upload {
			halt(RemoteExecArgs{Err: "--upload requires --file; it writes the script to the --file path on the server"})
		}
		if fromServer {
			halt(RemoteExecArgs{Err: "--from-server requires --file; it reads the script from the --file path on the server"})
		}
	} else {
		if len(env) > 0 {
			halt(RemoteExecArgs{
				Err: "--env cannot be combined with --file; a verified file takes no environment—set the variables inside the script",
			})
		}
		if fromServer && upload {
			halt(RemoteExecArgs{
				Err: "--from-server cannot be combined with --upload; --from-server submits the file already on the server, --upload writes a local copy there",
			})
		}
		if fromServer && fileFrom != "" {
			halt(RemoteExecArgs{
				Err: "--from-server cannot be combined with --file-from; the content is read from the --file path on the server, not from a local file",
			})
		}
		// The upload writes to the cleaned path while the agent opens the path as
		// given, and a symlink before ".." makes those two different files.
		if upload && pathpkg.Clean(filePath) != filePath {
			halt(RemoteExecArgs{
				Err: fmt.Sprintf("--upload needs a clean --file path, without '.', '..', or repeated or trailing slashes: %s", filePath),
			})
		}
		if len(commandParts) > 0 && !separated {
			halt(RemoteExecArgs{
				Err: "--file cannot be combined with a command line; put the script's arguments after --",
			})
		}
	}

	if stopped {
		stop.OutputFormat = outputFormat
		stop.UnsetEnv = unsetEnv
		return stop
	}

	if server != "" && strings.Contains(server, "@") && !strings.Contains(server, ":") {
		sshTarget := utils.ParseSSHTarget(server)
		if username == "" && sshTarget.User != "" {
			username = sshTarget.User
		}
		server = sshTarget.Host
	}

	parsed := RemoteExecArgs{
		Username:      username,
		Groupname:     groupname,
		WorkSessionID: workSessionID,
		OutputFormat:  outputFormat,
		Server:        server,
		Purpose:       purpose,
		Env:           env,
		UnsetEnv:      unsetEnv,
		WaitApproval:  waitApproval,
		Detach:        detach,
		Wait:          wait,
	}
	if filePath != "" {
		parsed.File = &FileExecArgs{
			Path:        filePath,
			From:        fileFrom,
			Interpreter: interpreter,
			Args:        commandParts,
			ReuseDays:   reuseDays,
			Upload:      upload,
			Overwrite:   overwrite,
			FromServer:  fromServer,
		}
		return parsed
	}
	parsed.Command = ShellJoin(commandParts)
	return parsed
}

// ParseEnvArg parses a --env token into env. A bare KEY reads the shell value and
// is returned as unset when there is none; malformed input returns an error message.
func ParseEnvArg(arg string, env map[string]string) (unset, errMsg string) {
	body, ok := strings.CutPrefix(arg, "--env=")
	var key, value string
	var hasValue bool
	if ok {
		key, value, hasValue = strings.Cut(trimMatchedQuotes(body), "=")
	}
	if !ok || key == "" {
		// Never echo arg: a malformed token like --env="=secret" would leak the value.
		return "", "invalid --env argument: use --env=KEY or --env=KEY=VALUE"
	}
	if hasValue {
		env[key] = value
		return "", ""
	}
	v, exists := os.LookupEnv(key)
	if !exists {
		return key, ""
	}
	env[key] = v
	return "", ""
}

// WarnUnsetEnv is the caller's half of ParseEnvArg: parsing stays silent because the
// --output pre-scan parses the same line before the command does.
func WarnUnsetEnv(keys []string) {
	for _, key := range keys {
		utils.CliWarning("no environment variable found for key '%s'", key)
	}
}

// WaitTimeout returns the effective approval-wait timeout, or 0 when not waiting.
func (a RemoteExecArgs) WaitTimeout() time.Duration {
	if a.WaitApproval > 0 {
		return a.WaitApproval
	}
	if a.Wait {
		return utils.DefaultApprovalWaitTimeout
	}
	return 0
}

// ShellJoin reassembles tokenized command parts into a single string.
// Parts containing whitespace or single quotes are re-quoted to preserve
// argument boundaries; metacharacters pass through for the remote shell.
// A single-element slice is returned as-is (already a formed command line).
func ShellJoin(parts []string) string {
	if len(parts) == 1 {
		return parts[0]
	}
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = shellQuote(p)
	}
	return strings.Join(out, " ")
}

// trimMatchedQuotes strips one matched leading+trailing double-quote pair only.
// A real shell already strips wrappers, so leaving unbalanced quotes intact
// avoids silently corrupting a value that legitimately ends in ".
func trimMatchedQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

// shellQuote wraps s in single quotes if it contains whitespace or a single quote.
// Whitespace signals that the local shell stripped outer quotes from a multi-word arg;
// metacharacters (|, $, *, etc.) pass through so the remote shell can interpret them.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n'") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// matchShortOrLongFlag checks whether arg is an exact match for the given short
// or long flag name, or a prefixed form (-uVALUE, --username=VALUE).
func matchShortOrLongFlag(arg, short, long string) bool {
	// Short flag: exact (-u), with = (-u=val), or attached value (-uroot)
	if arg == short || strings.HasPrefix(arg, short+"=") {
		return true
	}
	if len(arg) > len(short) && strings.HasPrefix(arg, short) && arg[len(short)] != '-' {
		return true
	}
	return arg == long || strings.HasPrefix(arg, long+"=")
}

// extractFlagValue extracts a flag value from one of three forms:
//   - attached short: -uroot          → "root"
//   - long with =:    --username=root → "root"
//   - separate:       -u root         → "root" (advances i)
//
// Returns the value, updated index, and an error message if no value was found.
func extractFlagValue(args []string, i int, short string) (string, int, string) {
	arg := args[i]
	if strings.Contains(arg, "=") {
		parts := strings.SplitN(arg, "=", 2)
		return parts[1], i, ""
	}
	if strings.HasPrefix(arg, short) && len(arg) > len(short) {
		return arg[len(short):], i, ""
	}
	if i+1 < len(args) {
		return args[i+1], i + 1, ""
	}
	return "", i, "flag needs an argument: " + arg
}
