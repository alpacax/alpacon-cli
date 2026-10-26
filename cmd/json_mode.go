package cmd

import (
	"os"
	"strings"

	"github.com/alpacax/alpacon-cli/cmd/exec"
	"github.com/alpacax/alpacon-cli/cmd/websh"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

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

// scanOutputFormat returns the command path and the --output value its parser will read.
func scanOutputFormat(args []string) (string, string) {
	target, rest, _ := RootCmd.Find(args)
	name := commandName(target)
	switch name {
	case "exec":
		return name, exec.ParseRemoteExecArgs(rest).OutputFormat
	case "websh":
		parsed, _ := websh.ParseWebshArgs(rest)
		return name, parsed.OutputFormat
	}
	return name, utils.PreScanOutputFormat(withoutFlagValues(target, args))
}

// routeHelpToStderr keeps stdout for data: under json a help screen is diagnostics.
func routeHelpToStderr() {
	defaultHelp := RootCmd.HelpFunc()
	RootCmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		if utils.OutputFormat == utils.OutputFormatJSON && c.OutOrStdout() == os.Stdout {
			c.SetOut(os.Stderr)
			defer c.SetOut(nil)
		}
		defaultHelp(c, args)
	})
}

// failUnknownHelpTopic makes "help nosuch" a usage_error under json; Cobra prints the usage and exits 0.
func failUnknownHelpTopic() {
	help, _, err := RootCmd.Find([]string{"help"})
	if err != nil || help == RootCmd {
		return
	}
	run := help.Run
	help.Run = func(c *cobra.Command, args []string) {
		if utils.OutputFormat == utils.OutputFormatJSON {
			if target, _, err := RootCmd.Find(args); target == nil || err != nil {
				utils.CliArgumentErrorWithExit("unknown help topic %q", strings.Join(args, " "))
			}
		}
		run(c, args)
	}
}

func prepareOutputMode(args []string) bool {
	RootCmd.InitDefaultHelpCmd()
	RootCmd.InitDefaultCompletionCmd()
	failUnknownHelpTopic()
	name, format := scanOutputFormat(args)
	utils.CommandName = name
	if format != utils.OutputFormatJSON {
		return false
	}
	utils.OutputFormat = utils.OutputFormatJSON
	RootCmd.SilenceErrors = true
	RootCmd.SilenceUsage = true
	return true
}
