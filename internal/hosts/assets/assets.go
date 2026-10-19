// Package assets embeds the Hermes plugin and the Pi extension, copied verbatim from the Python
// reference tree by tools/sync_assets.sh, except the Pi audit module pi/audit.mjs, and the OpenCode
// plugin (both maintained here; their unit tests live in internal/hosts/{pi,opencode}/testdata so they
// are never embedded). "all:" is needed because
// __init__.py starts with "_".
package assets

import "embed"

//go:embed all:hermes all:pi opencode/nod.js
var FS embed.FS
