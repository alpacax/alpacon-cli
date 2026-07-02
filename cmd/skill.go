package cmd

import (
	"fmt"
	"strings"

	"github.com/alpacax/alpacon-cli/skills"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Print the agent skill for the alpacon CLI",
	Long: `Print the Agent Skills (agentskills.io) markdown that teaches AI coding
agents how to use the alpacon CLI—work-session gating, exit codes, and the
JSON error contract.

Output goes to stdout with no decoration, so it can be redirected into an
agent's skill directory:

  mkdir -p ~/.claude/skills/alpacon && alpacon skill > ~/.claude/skills/alpacon/SKILL.md
  mkdir -p ~/.agents/skills/alpacon && alpacon skill > ~/.agents/skills/alpacon/SKILL.md`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Print(renderSkill())
	},
}

// renderSkill stamps the running CLI version into the embedded skill.
func renderSkill() string {
	return strings.Replace(skills.SkillMD, "cli-version: unknown", "cli-version: "+utils.Version, 1)
}
