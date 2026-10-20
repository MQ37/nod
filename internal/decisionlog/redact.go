package decisionlog

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// Command storage levels for the audit trail.
const (
	CommandsFull    = "full"    // the redacted command, clipped to MaxCommand characters (default)
	CommandsPreview = "preview" // the redacted command clipped to PreviewLen characters
	CommandsHash    = "hash"    // only the sha256
)

const (
	MaxCommand = 4000
	PreviewLen = 160
)

var secretPatterns = []struct {
	re  *regexp.Regexp
	rep string
}{
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|$)`), "<private-key>"},
	{regexp.MustCompile(`(?i)(Authorization\s*:\s*)(?:Bearer|Basic|token)?\s*[^\s'"]+`), "${1}<redacted>"},
	{regexp.MustCompile(`\bBearer\s+[A-Za-z0-9._~+/=-]{8,}`), "Bearer <redacted>"},
	{regexp.MustCompile(`\b(?:sk-|ghp_|gho_|ghs_|ghu_|github_pat_|glpat-|xox[a-z]-|apify_api_|AKIA|ASIA|eyJ)[A-Za-z0-9_\-.]{12,}`), "<tok>"},
	{regexp.MustCompile(`\b([A-Z0-9_]*(?:KEY|TOKEN|SECRET|PASSWORD|PASSWD|PASS)[A-Z0-9_]*=)(?:"[^"]*"|'[^']*'|\S+)`), "${1}<v>"},
}

// Redact removes obvious credentials from a command before it is stored. It is a safety net, not a
// guarantee: the audit file is also owner-only (0600).
func Redact(s string) string {
	for _, p := range secretPatterns {
		s = p.re.ReplaceAllString(s, p.rep)
	}
	return s
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// ContextLen is the clip of one stored user message.
const ContextLen = 300

// ContextFields redacts and clips the user messages stored with a verdict, keeping at most n of the
// newest. Empty messages are dropped.
func ContextFields(msgs []string, n int) []string {
	var out []string
	for _, m := range msgs {
		if m = strings.TrimSpace(m); m != "" {
			out = append(out, clipRunes(Redact(m), ContextLen))
		}
	}
	if n >= 0 && len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// SHA256 is the hex sha256 of the original (unredacted) command, so a known command can be matched to
// its audit line without trusting the stored text.
func SHA256(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// HashSession is a short, stable pseudonym of a host session id.
func HashSession(id string) string {
	if id == "" {
		return ""
	}
	return SHA256(id)[:16]
}

// CommandFields returns the (sha256, stored text) pair for a command at the given level. Level ""
// means CommandsFull, and so do unknown levels: the documented default.
func CommandFields(cmd, level string) (sha, text string) {
	if cmd == "" {
		return "", ""
	}
	sha = SHA256(cmd)
	switch strings.ToLower(strings.TrimSpace(level)) {
	case CommandsHash:
		return sha, ""
	case CommandsPreview:
		return sha, clipRunes(Redact(cmd), PreviewLen)
	}
	return sha, clipRunes(Redact(cmd), MaxCommand)
}
