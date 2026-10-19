// Package assets embeds the Hermes plugin and the Pi extension, copied verbatim from the Python
// reference tree by tools/sync_assets.sh, and the OpenCode plugin (maintained here; its unit tests
// live in internal/hosts/opencode/testdata so they are never embedded). "all:" is needed because
// __init__.py starts with "_".
package assets

import "embed"

//go:embed all:hermes all:pi opencode/nod.js
var FS embed.FS
