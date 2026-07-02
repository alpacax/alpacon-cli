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

	skillGateCodes = map[string]bool{
		utils.WorkSessionRequired:         true,
		utils.WorkSessionNotUsable:        true,
		utils.WorkSessionNotActive:        true,
		utils.WorkSessionExpired:          true,
		utils.WorkSessionScopeNotAllowed:  true,
		utils.WorkSessionServerNotAllowed: true,
		utils.WorkSessionAssigneeMismatch: true,
	}
)

// skillInvocations extracts `alpacon ...` lines from fenced code blocks,
// returning the tokens after "alpacon" for each invocation.
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
		trimmed = strings.TrimPrefix(trimmed, "$ ")
		if !inBlock || !strings.HasPrefix(trimmed, "alpacon ") {
			continue
		}
		invocations = append(invocations, strings.Fields(trimmed)[1:])
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
	re := regexp.MustCompile(`work_session_[a-z_]+`)
	found := map[string]bool{}
	for _, m := range re.FindAllString(skills.SkillMD, -1) {
		assert.True(t, skillGateCodes[m], "skill references unknown gate code %q", m)
		found[m] = true
	}
	for code := range skillGateCodes {
		assert.True(t, found[code], "skill missing gate code %q", code)
	}
}

func TestSkillFrontmatter(t *testing.T) {
	assert.True(t, strings.HasPrefix(skills.SkillMD, "---\n"), "frontmatter must open the file")
	assert.Contains(t, skills.SkillMD, "\nname: alpacon\n", "name must match the skill directory")
	assert.Contains(t, skills.SkillMD, "description: >-")
	assert.Contains(t, skills.SkillMD, "cli-version: unknown", "version placeholder required for 'alpacon skill' stamping")
}
