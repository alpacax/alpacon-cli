// Package skills embeds the Agent Skills content shipped with the CLI.
package skills

import _ "embed"

// SkillMD is the alpacon agent skill (skills/alpacon/SKILL.md) verbatim.
//
//go:embed alpacon/SKILL.md
var SkillMD string
