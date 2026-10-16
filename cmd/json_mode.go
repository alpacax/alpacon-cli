package cmd

import (
	"os"
	"strings"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// remoteValueFlags take a separate value token in exec and websh; the scan must hop over it.
var remoteValueFlags = map[string]bool{
	"-u": true, "--username": true, "-g": true, "--groupname": true,
	"--work-session": true, "--purpose": true, "--output": true,
	"--file": true, "--file-from": true, "--interpreter": true,
	"--reuse-days": true, "--wait-approval": true,
}

// websshOwnArgs mirrors websh.ParseWebshArgs: it keeps parsing flags after the server and
// ends at the first token no flag case claims once the server is known.
func websshOwnArgs(rest []string) []string {
	serverSeen := false
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		switch {
		case arg == "-s" || arg == "--share" || arg == "-h" || arg == "--help" ||
			arg == "--env" || strings.HasPrefix(arg, "--env="):
		case strings.HasPrefix(arg, "-u") || strings.HasPrefix(arg, "--username") ||
			strings.HasPrefix(arg, "-g") || strings.HasPrefix(arg, "--groupname") ||
			arg == "--work-session" || strings.HasPrefix(arg, "--work-session=") ||
			arg == "--output" || strings.HasPrefix(arg, "--output="):
			if !strings.Contains(arg, "=") && i+1 < len(rest) {
				i++
			}
		case strings.HasPrefix(arg, "--read-only"):
			if next := strings.ToLower(strings.TrimSpace(argAt(rest, i+1))); !strings.Contains(arg, "=") && (next == "true" || next == "false") {
				i++
			}
		case serverSeen:
			return rest[:i]
		default:
			serverSeen = true
		}
	}
	return rest
}

func argAt(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}

// withoutFlagValues drops the separate value token of every flag the command defines as
// taking one, so pflag's reading of "--type --output=json" is the scan's. --output keeps its value.
func withoutFlagValues(target *cobra.Command, args []string) []string {
	if target == nil {
		return args
	}
	local, inherited := target.LocalFlags(), target.InheritedFlags()
	find := func(name string, short bool) *pflag.Flag {
		for _, set := range []*pflag.FlagSet{local, inherited} {
			lookup := set.Lookup
			if short {
				lookup = set.ShorthandLookup
			}
			if f := lookup(name); f != nil {
				return f
			}
		}
		return nil
	}
	// valueFlag returns the flag whose value is the token after arg, as pflag reads it.
	valueFlag := func(arg string) *pflag.Flag {
		if name, isLong := strings.CutPrefix(arg, "--"); isLong {
			if name == "" || strings.Contains(name, "=") {
				return nil
			}
			if f := find(name, false); f != nil && f.NoOptDefVal == "" {
				return f
			}
			return nil
		}
		for i := 1; i < len(arg) && arg[0] == '-'; i++ {
			f := find(arg[i:i+1], true)
			switch {
			case f == nil:
				return nil
			case f.NoOptDefVal != "":
				continue
			case i+1 < len(arg):
				return nil
			}
			return f
		}
		return nil
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		out = append(out, args[i])
		if args[i] == "--" {
			return append(out, args[i+1:]...)
		}
		f := valueFlag(args[i])
		if f == nil || i+1 >= len(args) {
			continue
		}
		i++
		if f.Name == "output" {
			out = append(out, args[i])
		}
	}
	return out
}

// commandName is the canonical path without the root name; aliases resolve to Name().
func commandName(c *cobra.Command) string {
	if c == nil || c == RootCmd {
		return ""
	}
	return strings.TrimPrefix(c.CommandPath(), RootCmd.Name()+" ")
}

// ownFlagArgs keeps the leading flags of an exec or websh line up to the server; every token
// after the server belongs to the remote command, and so does everything after "--".
func ownFlagArgs(rest []string) []string {
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		switch {
		case arg == "--":
			return rest[:i]
		case remoteValueFlags[arg]:
			i++
		case arg == "--read-only" && i+1 < len(rest) && (rest[i+1] == "true" || rest[i+1] == "false"):
			i++
		case !strings.HasPrefix(arg, "-"):
			return rest[:i]
		}
	}
	return rest
}

var websshHoppedFlags = map[string]bool{
	"-u": true, "--username": true, "-g": true, "--groupname": true,
	"--work-session": true, "--output": true,
}

// withoutRemoteValues drops the separate value token of each flag in valueFlags, as both
// parsers consume it whatever it looks like; --output keeps its own.
func withoutRemoteValues(own []string, valueFlags map[string]bool) []string {
	out := make([]string, 0, len(own))
	for i := 0; i < len(own); i++ {
		out = append(out, own[i])
		if !valueFlags[own[i]] || i+1 >= len(own) {
			continue
		}
		i++
		if own[i-1] == "--output" {
			out = append(out, own[i])
		}
	}
	return out
}

func scanArgs(args []string) (string, []string) {
	target, rest, _ := RootCmd.Find(args)
	name := commandName(target)
	switch name {
	case "exec":
		return name, withoutRemoteValues(ownFlagArgs(rest), remoteValueFlags)
	case "websh":
		return name, withoutRemoteValues(websshOwnArgs(rest), websshHoppedFlags)
	}
	return name, withoutFlagValues(target, args)
}

// routeHelpToStderr keeps stdout for data: under json a help screen is diagnostics.
func routeHelpToStderr() {
	defaultHelp := RootCmd.HelpFunc()
	RootCmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		if utils.OutputFormat == utils.OutputFormatJSON && c.OutOrStdout() == os.Stdout {
			c.SetOut(os.Stderr)
			defer c.SetOut(os.Stdout)
		}
		defaultHelp(c, args)
	})
}

func prepareOutputMode(args []string) bool {
	name, scan := scanArgs(args)
	utils.CommandName = name
	if utils.PreScanOutputFormat(scan) != utils.OutputFormatJSON {
		return false
	}
	utils.OutputFormat = utils.OutputFormatJSON
	RootCmd.SilenceErrors = true
	RootCmd.SilenceUsage = true
	return true
}
