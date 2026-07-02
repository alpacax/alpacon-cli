package cmd

import (
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

	// inlineAlpaconSpan matches inline single-backtick `alpacon ...` references
	// in prose (as opposed to fenced-block invocations).
	inlineAlpaconSpan = regexp.MustCompile("`(alpacon [^`]+)`")
)

// skillInvocations extracts `alpacon ...` command references from the skill
// doc—both fenced-block lines and inline single-backtick spans—returning the
// tokens after "alpacon" for each invocation.
func skillInvocations(t *testing.T) [][]string {
	t.Helper()
	var invocations [][]string
	inBlock := false
	for _, line := range strings.Split(skills.SkillMD, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inBlock = !inBlock
			continue
		}
		if inBlock {
			trimmed = strings.TrimPrefix(trimmed, "$ ")
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
				// A group command must be followed by a real subcommand;
				// a leaf command's trailing tokens are positional args.
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
	assert.NotNil(t, RootCmd.PersistentFlags().Lookup("output"))

	ws := findSubcommand(RootCmd, "work-session")
	require.NotNil(t, ws)
	create := findSubcommand(ws, "create")
	require.NotNil(t, create)
	for _, name := range []string{"purpose", "scope", "server", "expires-in", "requester-type", "sudo", "wait"} {
		assert.NotNil(t, create.Flags().Lookup(name), "work-session create --%s missing", name)
	}
}

func TestSkillGateCodesMatchCLI(t *testing.T) {
	gateCodes := make(map[string]bool, len(utils.WorkSessionGateCodes))
	for _, code := range utils.WorkSessionGateCodes {
		gateCodes[code] = true
	}

	re := regexp.MustCompile(`work_session_[a-z_]+`)
	found := map[string]bool{}
	for _, m := range re.FindAllString(skills.SkillMD, -1) {
		assert.True(t, gateCodes[m], "skill references unknown gate code %q", m)
		found[m] = true
	}
	for _, code := range utils.WorkSessionGateCodes {
		assert.True(t, found[code], "skill missing gate code %q", code)
	}
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
