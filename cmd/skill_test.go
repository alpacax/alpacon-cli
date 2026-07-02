package cmd

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/skills"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	subcommandToken = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

	// inlineAlpaconSpan matches inline single-backtick `alpacon ...` spans; fenced blocks are handled separately.
	inlineAlpaconSpan = regexp.MustCompile("`(alpacon [^`]+)`")
)

// skillInvocations returns the tokens after "alpacon" for each command reference in the skill doc, joining backslash-continued lines so multi-line commands keep all their flags.
func skillInvocations(t *testing.T) [][]string {
	t.Helper()
	var invocations [][]string
	inBlock := false
	var cont string // accumulates a backslash-continued command line
	for _, line := range strings.Split(skills.SkillMD, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inBlock = !inBlock
			cont = ""
			continue
		}
		if inBlock {
			if cont != "" {
				trimmed = cont + " " + trimmed
				cont = ""
			}
			if strings.HasSuffix(trimmed, "\\") {
				cont = strings.TrimSpace(strings.TrimSuffix(trimmed, "\\"))
				continue
			}
			if strings.HasPrefix(trimmed, "alpacon ") {
				invocations = append(invocations, strings.Fields(trimmed)[1:])
			}
			continue
		}
		for _, m := range inlineAlpaconSpan.FindAllStringSubmatch(line, -1) {
			invocations = append(invocations, strings.Fields(m[1])[1:])
		}
	}
	require.NotEmpty(t, invocations, "skill must contain alpacon command examples")
	return invocations
}

func findSubcommand(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name || c.HasAlias(name) {
			return c
		}
	}
	return nil
}

// skillFlagDefined reports whether the flag token is defined on cmd or any ancestor, covering inherited persistent flags like --output.
func skillFlagDefined(cmd *cobra.Command, token string) bool {
	name := strings.TrimLeft(strings.SplitN(token, "=", 2)[0], "-")
	long := strings.HasPrefix(token, "--")
	for c := cmd; c != nil; c = c.Parent() {
		if long {
			if c.Flags().Lookup(name) != nil || c.PersistentFlags().Lookup(name) != nil {
				return true
			}
		} else if c.Flags().ShorthandLookup(name) != nil || c.PersistentFlags().ShorthandLookup(name) != nil {
			return true
		}
	}
	return false
}

func TestSkillSubcommandsExist(t *testing.T) {
	for _, tokens := range skillInvocations(t) {
		cur := RootCmd
		path := "alpacon"
		for _, tok := range tokens {
			if !subcommandToken.MatchString(tok) {
				break // flag, placeholder, or shell arg—stop walking
			}
			next := findSubcommand(cur, tok)
			if next == nil {
				// A group command needs a real subcommand next; a leaf's trailing tokens are positional args.
				if cur == RootCmd || len(cur.Commands()) > 0 {
					t.Errorf("skill references unknown command %q after %q", tok, path)
				}
				break
			}
			cur = next
			path += " " + tok
		}
	}
}

func TestSkillFlagsExist(t *testing.T) {
	for _, tokens := range skillInvocations(t) {
		cmd := RootCmd
		flagsStarted := false
		for _, tok := range tokens {
			switch {
			case strings.HasPrefix(tok, "-"):
				flagsStarted = true
				// DisableFlagParsing commands (e.g. exec) hand-roll flags, so they're not in cobra's FlagSet; their own parser tests cover them.
				if cmd.DisableFlagParsing {
					continue
				}
				assert.True(t, skillFlagDefined(cmd, tok),
					"skill references flag %q not defined on %q", tok, cmd.CommandPath())
			case !flagsStarted && subcommandToken.MatchString(tok):
				if next := findSubcommand(cmd, tok); next != nil {
					cmd = next
				}
			}
		}
	}

	// --wait and --sudo appear only in prose, not in an `alpacon ...` invocation, so the loop cannot reach them.
	create := findSubcommand(findSubcommand(RootCmd, "work-session"), "create")
	require.NotNil(t, create)
	for _, name := range []string{"sudo", "wait"} {
		assert.NotNil(t, create.Flags().Lookup(name), "work-session create --%s missing", name)
	}
}

func TestSkillFlagDefinedRejectsUnknown(t *testing.T) {
	assert.True(t, skillFlagDefined(RootCmd, "--output"), "persistent --output must resolve")
	assert.False(t, skillFlagDefined(RootCmd, "--nonexistent"), "unknown flag must not resolve")
}

func TestSkillGateCodesMatchCLI(t *testing.T) {
	// Keep in sync with the utils.WorkSession* gate constants.
	wantCodes := []string{
		utils.WorkSessionRequired,
		utils.WorkSessionNotUsable,
		utils.WorkSessionNotActive,
		utils.WorkSessionExpired,
		utils.WorkSessionScopeNotAllowed,
		utils.WorkSessionServerNotAllowed,
		utils.WorkSessionAssigneeMismatch,
	}
	gateCodes := make(map[string]bool, len(wantCodes))
	for _, code := range wantCodes {
		gateCodes[code] = true
	}

	re := regexp.MustCompile(`work_session_[a-z_]+`)
	found := map[string]bool{}
	for _, m := range re.FindAllString(skills.SkillMD, -1) {
		assert.True(t, gateCodes[m], "skill references unknown gate code %q", m)
		found[m] = true
	}
	for _, code := range wantCodes {
		assert.True(t, found[code], "skill missing gate code %q", code)
	}
}

func TestSkillExitCodesMatchCLI(t *testing.T) {
	assert.Contains(t, skills.SkillMD, fmt.Sprintf("| `%d` | work-session gate denied", utils.ExitCodeWorkSessionDenied))
	assert.Contains(t, skills.SkillMD, fmt.Sprintf("| `%d` | pending human approval", utils.ExitCodePendingApproval))
}

func TestSkillFrontmatter(t *testing.T) {
	assert.True(t, strings.HasPrefix(skills.SkillMD, "---\n"), "frontmatter must open the file")
	assert.Contains(t, skills.SkillMD, "\nname: alpacon\n", "name must match the skill directory")
	assert.Contains(t, skills.SkillMD, "description: >-")
	assert.Contains(t, skills.SkillMD, "cli-version: unknown", "version placeholder required for 'alpacon skill' stamping")
}

func TestRenderSkillStampsVersion(t *testing.T) {
	rendered := renderSkill()
	assert.NotContains(t, rendered, "cli-version: unknown")
	assert.Contains(t, rendered, "cli-version: "+utils.Version)
}

func TestSkillCommandRegistered(t *testing.T) {
	assert.NotNil(t, findSubcommand(RootCmd, "skill"))
}
