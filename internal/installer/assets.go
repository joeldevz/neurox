package installer

import _ "embed"

// neuroxSkillContent is the SKILL.md file embedded at build time.
// It is installed to ~/.claude/skills/neurox/SKILL.md during `neurox install`
// so Claude Code loads it automatically.
//
//go:embed skill.md
var neuroxSkillContent []byte
