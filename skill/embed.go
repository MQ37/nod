// Package skill embeds the agent skill (SKILL.md). It lives next to the file because go:embed cannot reach outside its package directory.
package skill

import _ "embed"

// Markdown is the content of SKILL.md.
//
//go:embed SKILL.md
var Markdown string
