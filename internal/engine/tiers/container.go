package tiers

import (
	"regexp"
	"strings"

	"github.com/MQ37/nod/internal/engine/pycompat"
)

var (
	netArg     = pycompat.Compile(`(?:^|\s)--net(?:work)?(?:=|\s+)(\S+)`, "")
	badRun     = pycompat.MustCompile(`(?:^|\s)(?:--privileged|--cap-add|--device|--pid[= ]|--ipc[= ]|--uts[= ]|--userns[= ]|--security-opt[= ]\S*(?:unconfined|disable)|--volumes-from|--pod|--rootfs|--runtime|--hooks-dir|--conmon|--url|--connection|--remote|--systemd|--cgroups|--mount=type=(?:$|[^b]|b(?:$|[^i]|i(?:$|[^n]|n(?:$|[^d])))))`, "")
	sockHit    = pycompat.MustCompile(`docker\.sock|/run/podman|/var/run`, "")
	mountSrc   = pycompat.Compile(`(?:src|source)=([^,]+)`, "")
	podmanWord = pycompat.MustCompile(`\b(?:podman|docker)\b`, "")
	badPrefix  = []string{"--runtime", "--hooks-dir", "--conmon", "--url", "--connection", "--remote", "--identity"}
)

func mountSrcOK(src, cwd string) bool {
	if src == "" {
		return false
	}
	if !strings.Contains(src, "/") && !strings.HasPrefix(src, "~") && !strings.HasPrefix(src, "$") {
		return true
	}
	if strings.HasPrefix(src, "$PWD") || strings.HasPrefix(src, "${PWD}") {
		return !sensitive.Match(src) && !strings.Contains(src, "/..")
	}
	if strings.HasPrefix(src, "~") || strings.HasPrefix(src, "$") || strings.Contains(src, "$") {
		return false
	}
	q, ok := norm(src, cwd)
	if !ok || sensitive.Match(q) {
		return false
	}
	return localPath(q, cwd, true)
}

func containerRunOK(rest []string, cwd string) bool {
	flat := strings.Join(rest, " ")
	nets := netGroups(netArg, flat)
	if len(nets) != 1 || nets[0] != "none" {
		return false
	}
	if badRun.Match(flat) || sockHit.Match(flat) {
		return false
	}
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		spec := ""
		hasSpec := false
		switch {
		case (a == "-v" || a == "--volume") && i+1 < len(rest):
			spec = rest[i+1]
			hasSpec = true
			i++
		case strings.HasPrefix(a, "--volume="):
			spec = a[len("--volume="):]
			hasSpec = true
		case strings.HasPrefix(a, "-v") && len(a) > 2 && !strings.HasPrefix(a, "--"):
			spec = a[2:]
			hasSpec = true
		}
		if hasSpec {
			parts := strings.Split(spec, ":")
			if !mountSrcOK(parts[0], cwd) {
				return false
			}
			continue
		}
		if a == "--mount" || strings.HasPrefix(a, "--mount=") {
			m := ""
			if strings.HasPrefix(a, "--mount=") {
				m = a[len("--mount="):]
			} else if i+1 < len(rest) {
				m = rest[i+1]
			}
			if a == "--mount" {
				i++
			}
			if strings.Contains(m, "type=") && !strings.Contains(m, "type=bind") && !strings.Contains(m, "type=tmpfs") && !strings.Contains(m, "type=volume") {
				return false
			}
			if sm := mountSrc.FindStringSubmatch(m); sm != nil && !mountSrcOK(sm[1], cwd) {
				return false
			}
		}
	}
	return true
}

func netGroups(re *regexp.Regexp, s string) []string {
	all := re.FindAllStringSubmatch(s, -1)
	out := make([]string, len(all))
	for i, m := range all {
		out[i] = m[1]
	}
	return out
}

// ContainersAllHardened reports whether cmd contains at least one podman/docker invocation and every one is a sandboxed run, a read-only subcommand, or an unprivileged exec.
func ContainersAllHardened(cmd, cwd string) bool {
	bodies := pycompat.FindHeredocs(cmd, false)
	for _, h := range bodies {
		if h.HeaderEnd <= len(cmd) && podmanWord.Match(cmd[h.HeaderEnd:h.BodyEnd]) {
			return false
		}
	}
	stripped := stripHardenedHeredocs(cmd, bodies)
	units, ok := lex(stripped)
	if !ok {
		return false
	}
	seen := false
	roSubs := map[string]bool{"ps": true, "images": true, "logs": true, "version": true, "info": true, "top": true, "port": true, "stats": true, "diff": true, "history": true, "events": true}
	for _, u := range units {
		if len(u.words) == 0 {
			continue
		}
		p := pycompat.Basename(u.words[0].text)
		if p != "podman" && p != "docker" {
			continue
		}
		seen = true
		texts := wordTexts(u.words[1:])
		sub := ""
		if len(texts) > 0 && !strings.HasPrefix(texts[0], "-") {
			sub = texts[0]
		}
		if roSubs[sub] {
			continue
		}
		if sub == "run" && containerRunOK(texts[1:], cwd) {
			continue
		}
		if sub == "exec" && !execUnsafe(texts[1:]) {
			continue
		}
		return false
	}
	return seen
}

func execUnsafe(args []string) bool {
	for _, t := range args {
		if t == "--privileged" || t == "-u" || t == "--user" || t == "--privileged=true" || strings.HasPrefix(t, "--user=") || strings.HasPrefix(t, "-u") {
			return true
		}
	}
	return false
}

func stripHardenedHeredocs(cmd string, hs []pycompat.Heredoc) string {
	if len(hs) == 0 {
		return cmd
	}
	var b strings.Builder
	pos := 0
	for _, h := range hs {
		if h.Start < pos || h.End > len(cmd) || h.Start > len(cmd) {
			continue
		}
		b.WriteString(cmd[pos:h.Start])
		b.WriteString(" \n")
		pos = h.End
	}
	b.WriteString(cmd[pos:])
	return strings.ReplaceAll(b.String(), "<HEREDOC>", "")
}
