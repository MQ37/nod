// Package assets embeds the Hermes plugin, the Pi extension and the OpenCode plugin. They are
// maintained here (the Python reference tree they once came from is no longer synced); their unit
// tests live in internal/hosts/*/pytests and internal/hosts/*/testdata so they are never embedded.
// "all:" is needed because __init__.py starts with "_".
package assets

import "embed"

//go:embed all:hermes all:pi opencode/nod.js
var FS embed.FS
