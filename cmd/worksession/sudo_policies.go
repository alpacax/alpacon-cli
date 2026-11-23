package worksession

import (
	"encoding/json"

	wsapi "github.com/alpacax/alpacon-cli/api/worksession"
	"github.com/alpacax/alpacon-cli/utils"
)

func warnBroadSudoPolicies(ios *utils.IOStreams, policies []wsapi.SudoPolicyInline) {
	var patterns []string
	for _, policy := range policies {
		if policy.BroadPatterns != nil {
			patterns = append(patterns, (*policy.BroadPatterns)...)
		}
	}
	if len(patterns) > 0 {
		ios.Warning("These sudo patterns may allow arbitrary commands as root once authorized. Patterns: %q", patterns)
	}
}

func warnUpdatedSudoPolicies(ios *utils.IOStreams, previous []wsapi.SudoPolicyInline, updated *wsapi.WorkSession, queued bool) {
	existing := make(map[string]struct{}, len(previous))
	for _, policy := range previous {
		existing[policy.ID] = struct{}{}
	}

	policies := updated.SudoPolicies
	if pending := updated.PendingModificationRequest; queued && pending != nil {
		var changes struct {
			SudoPolicies []wsapi.SudoPolicyInline `json:"sudo_policies"`
		}
		if json.Unmarshal(pending.Changes, &changes) == nil && changes.SudoPolicies != nil {
			policies = changes.SudoPolicies
		}
	}
	var added []wsapi.SudoPolicyInline
	for _, policy := range policies {
		if _, found := existing[policy.ID]; policy.ID == "" || !found {
			added = append(added, policy)
		}
	}
	warnBroadSudoPolicies(ios, added)
}
