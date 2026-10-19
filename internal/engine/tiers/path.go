package tiers

import (
	"strings"

	"github.com/MQ37/nod/internal/engine/pycompat"
)

var (
	sensitive = pycompat.MustCompile(`(\.ssh\b|\.env\b|(^|/)\.env\.|credential|id_rsa|id_ed25519|\.aws\b|\.kube\b|\.gnupg\b|\.netrc|\.npmrc|\.pypirc|\.pem\b|\.key\b|\.p12\b|\.pfx\b|token|secret|passw|/etc/shadow|/etc/sudoers|\.git-credentials|keychain|wallet|private[_-]?key|\.docker/config|gh/hosts|\.gem/credentials|/proc/[^ ]*/environ|\.claude/(settings|hooks|skills)|\.hermes|\.bash_history|\.zsh_history|authorized_keys|apify_api_|\bghp_|\bgho_|\bsk-[A-Za-z0-9]|\bAKIA[A-Z0-9]|\bxox[bap]-|BEGIN [A-Z ]*PRIVATE|\beyJ[A-Za-z0-9_-]{10}|\.local/share/(?:toolguard|nod)|(?:toolguard|nod)/(events|traces)|lockbox|\.mozilla|\.config/(google-chrome|chromium|BraveSoftware|op|rclone|gcloud|doctl)|\.local/share/keyrings|\.password-store|\.thunderbird|\.codex|\.openai|\.anthropic|\.claude\.json|\.git/(config|hooks)|\.gitconfig|\.config/git)`, "i")
	envSecret = pycompat.MustCompile(`\$\{?[A-Za-z0-9_]*(KEY|TOKEN|SECRET|PASS|CRED|AUTH)[A-Za-z0-9_]*`, "i")
	execCfg   = pycompat.MustCompile(`(^|/)(\.git(/|$)|\.gitattributes$|\.gitmodules$|\.husky(/|$)|\.pre-commit-config\.yaml$|\.github/workflows/|Makefile$|makefile$|GNUmakefile$|package\.json$|setup\.py$|setup\.cfg$|pyproject\.toml$|\.npmrc$|\.yarnrc|conftest\.py$|noxfile\.py$|tox\.ini$|Containerfile$|Dockerfile$|\.envrc$|\.vscode/|\.cargo/config|build\.rs$|go\.work$|(^|/)\.(bash|zsh)[\w]*$|(^|/)\.(profile|inputrc|xinitrc|xprofile|zlogin|zlogout|login|exrc|vimrc|tmux\.conf|screenrc|gitconfig)$|\.config/autostart)`, "")
	safeAsg   = pycompat.MustCompile(`^(LC_[A-Z_]+|LANG|TZ|NO_COLOR|FORCE_COLOR|CI|DEBUG|VERBOSE|CGO_ENABLED|GOOS|GOARCH|NODE_ENV|PYTHONDONTWRITEBYTECODE|COLUMNS|LINES)$`, "")
	homeUser  = pycompat.MustCompile(`^/home/[^/]+/?$`, "")
	homeMac   = pycompat.MustCompile(`^/Users/[^/]+/?$`, "")
	readOut   = pycompat.MustCompile(`^/(usr|etc/(os-release|hostname|hosts|fstab|lsb-release|issue|resolv\.conf)|proc/(cpuinfo|meminfo|loadavg|version|uptime|mounts)|sys/class|var/log|opt|lib|bin|sbin|dev/(null|zero|shm)|run/user|home/[^/]+/(workdir|go/pkg|\.cache))(/|$)`, "")
)

var scratch = []string{"/tmp", "/dev/shm", "/var/tmp"}

var buildDirs = map[string]bool{
	"node_modules": true, "dist": true, "build": true, ".cache": true, "__pycache__": true,
	"target": true, ".next": true, "out": true, ".pytest_cache": true, "coverage": true, ".turbo": true,
}

func norm(p, cwd string) (string, bool) {
	if p == "" {
		return "", false
	}
	for _, ch := range "$`{}\\\n" {
		if strings.ContainsRune(p, ch) {
			return "", false
		}
	}
	if strings.HasPrefix(p, "~") {
		return "", false
	}
	base := cwd
	if base == "" {
		base = "/_nocwd_"
	}
	q := p
	if !strings.HasPrefix(p, "/") {
		q = pycompat.Join(base, p)
	}
	return pycompat.Normpath(q), true
}

func homeLike(cwd string) bool {
	if cwd == "" || cwd == "/" || cwd == "/root" {
		return true
	}
	return homeUser.Match(cwd) || homeMac.Match(cwd)
}

func localPath(p, cwd string, allowScratch bool) bool {
	q, ok := norm(p, cwd)
	if !ok || sensitive.Match(q) {
		return false
	}
	if allowScratch {
		for _, s := range scratch {
			if q == s || strings.HasPrefix(q, s+"/") {
				return true
			}
		}
	}
	if homeLike(cwd) {
		return false
	}
	root := strings.TrimRight(cwd, "/")
	return q == root || strings.HasPrefix(q, root+"/")
}

func risky(w word) bool {
	if sensitive.Match(w.text) || envSecret.Match(w.text) {
		return true
	}
	if w.glob && (strings.HasPrefix(w.text, "~") || strings.Contains(w.text, "/.") || strings.HasPrefix(w.text, ".")) {
		return true
	}
	return false
}

func readableOutside(t string) bool {
	t = pycompat.Normpath(t)
	if strings.Contains(t, "//") {
		return false
	}
	for _, p := range strings.Split(t, "/") {
		if p == ".." {
			return false
		}
	}
	return readOut.Match(t) && !sensitive.Match(t)
}
