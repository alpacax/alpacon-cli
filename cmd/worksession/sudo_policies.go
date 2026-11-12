package worksession

import (
	"path"
	"strings"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/utils"
)

// broadSudoPatterns returns the original patterns that allow arbitrary root commands.
// An empty command list is represented as "all commands".
func broadSudoPatterns(policies []wsapi.SudoPolicyInline) []string {
	var broad []string
	for _, policy := range policies {
		if !policy.SessionAuthorized {
			continue
		}
		if len(policy.Commands) == 0 {
			broad = append(broad, "all commands")
			continue
		}
		for _, pattern := range policy.Commands {
			words := strings.Fields(pattern)
			if len(words) == 0 {
				continue
			}
			// The server expands * to any characters, so a starred program can be a shell.
			if strings.Contains(words[0], "*") {
				broad = append(broad, pattern)
				continue
			}
			switch path.Base(words[0]) {
			case "bash", "sh", "zsh", "dash", "ksh", "fish", "csh", "tcsh", "sudo", "su":
				broad = append(broad, pattern)
			}
		}
	}
	return broad
}

func warnBroadSudoPolicies(policies []wsapi.SudoPolicyInline) {
	if patterns := broadSudoPatterns(policies); len(patterns) > 0 {
		utils.CliWarning("This allows any command as root. Session-authorized sudo runs matching commands without MFA or approval. Patterns: %q", patterns)
	}
}
