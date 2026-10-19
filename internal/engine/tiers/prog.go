package tiers

import (
	"strings"

	"github.com/MQ37/nod/internal/engine/pycompat"
)

var (
	jqBad     = pycompat.MustCompile(`\benv\b|\$ENV|\binputs?\b|\bimport\b|\binclude\b|\$__|\bdebug\b|\bstderr\b|input_filename|\bgetpath\b|\$__loc__|\bhalt_error\b|\bsplits?\b|\bltrimstr\b`, "")
	findBad   = pycompat.MustCompile(`^-(?:exec|execdir|ok|okdir|delete|fprint\w*|fls|fprintf|prune)$`, "")
	findOK    = pycompat.MustCompile(`^-(?:name|iname|type|maxdepth|mindepth|path|ipath|regex|iregex|size|mtime|mmin|atime|amin|ctime|cmin|newer|not|o|a|and|or|print|print0|empty|user|group|perm|executable|readable|writable|lname|ilname|samefile|inum|links|depth|follow|xdev|mount|L|H|P|true|false|wholename|iwholename|prune|newermt|newerct|newerat)$`, "")
	sedAddr   = pycompat.MustCompile(`^(?:(?:\d+|\$)(?:,(?:\d+|\$))?)?(?:p|d|q)$`, "s")
	sedNumQ   = pycompat.MustCompile(`^\d+q$`, "")
	sedFlags  = pycompat.MustCompile(`^[gipI0-9]*$`, "")
	sedOpt    = pycompat.MustCompile(`^-[nEr]+$`, "")
	cpOpt     = pycompat.MustCompile(`^-(?:[rRapvnfiuTl]+|-(?:recursive|archive|preserve|verbose|no-clobber|update))$`, "")
	lnSym     = pycompat.MustCompile(`^-(?:[a-z]*s[a-z]*|-symbolic)$`, "")
	chmodMode = pycompat.MustCompile(`^(?:[ugoa]*[+=-][rwxXst]+|[0-7]{3,4})$`, "")
	rmFlag    = pycompat.MustCompile(`^-(?:[rRfiv]+|-(?:recursive|force|verbose))$`, "")
	rmRec     = pycompat.MustCompile(`^-(?:[a-z]*[rR][a-z]*|-recursive)$`, "")
	outFlag   = pycompat.MustCompile(`(?:^|-)(?:o|out|outdir|outfile|output|prefix|manifest-path|C|cwd|target-dir|config|w|write)$`, "")
	jobFlag   = pycompat.MustCompile(`^-j\d+$`, "")
	curlURL   = pycompat.MustCompile(`^https?://(?:localhost|127\.0\.0\.1|\[::1\])(?::\d+)?(?:/|$)`, "")
	ghMut     = pycompat.MustCompile(`(?:^|\s)(?:-X\s*\S+|--method\S*|-f\b|-F\b|--field|--raw-field|--input|--hostname|-H\b|--header|--template|-t\b)`, "")
	ghGet     = pycompat.MustCompile(`(?:^|\s)(?:-X\s*GET)\b`, "")
)

var gitRO = map[string]bool{
	"status": true, "diff": true, "log": true, "show": true, "rev-parse": true, "ls-files": true, "blame": true,
	"describe": true, "shortlog": true, "ls-tree": true, "cat-file": true, "grep": true, "name-rev": true,
	"merge-base": true, "rev-list": true, "diff-tree": true, "show-ref": true, "count-objects": true,
	"for-each-ref": true, "check-ignore": true,
}

var gitBadLong = []string{"--output", "--ext-diff", "--textconv", "--open-files-in-pager", "--exec-path", "--git-dir", "--work-tree", "--config", "--config-env", "--upload-pack", "--receive-pack", "--exec", "--paginate", "--no-pager-x"}

var buildPairs = map[[2]string]bool{
	{"go", "build"}: true, {"go", "test"}: true, {"go", "vet"}: true, {"go", "fmt"}: true, {"go", "list"}: true,
	{"go", "version"}: true, {"go", "doc"}: true, {"cargo", "build"}: true, {"cargo", "test"}: true, {"cargo", "check"}: true,
	{"cargo", "fmt"}: true, {"cargo", "clippy"}: true, {"npm", "test"}: true, {"npm", "ls"}: true, {"pnpm", "test"}: true,
	{"yarn", "test"}: true, {"python3", "-m"}: true, {"python", "-m"}: true,
}

var buildAny = map[string]bool{
	"gofmt": true, "goimports": true, "pytest": true, "tsc": true, "eslint": true, "prettier": true, "black": true, "ruff": true, "mypy": true,
}

var ghRead = map[string]map[string]bool{
	"pr":       {"view": true, "diff": true, "list": true, "status": true, "checks": true},
	"issue":    {"view": true, "list": true, "status": true},
	"repo":     {"view": true, "list": true},
	"release":  {"view": true, "list": true},
	"run":      {"view": true, "list": true, "watch": true},
	"workflow": {"view": true, "list": true},
	"label":    {"list": true},
	"search":   {"repos": true, "issues": true, "prs": true, "code": true, "commits": true},
	"auth":     {"status": true},
}

var curlLong = map[string]bool{
	"--silent": true, "--show-error": true, "--location": true, "--head": true, "--include": true, "--verbose": true,
	"--fail": true, "--get": true, "--globoff": true, "--compressed": true, "--no-progress-meter": true,
	"--http1.1": true, "--http2": true, "--ipv4": true, "--ipv6": true, "--fail-with-body": true,
	"--max-time": true, "--connect-timeout": true, "--retry": true, "--retry-delay": true, "--retry-max-time": true,
	"--user-agent": true, "--write-out": true,
}

var curlValue = map[string]bool{
	"--max-time": true, "--connect-timeout": true, "--retry": true, "--retry-delay": true, "--retry-max-time": true,
	"--user-agent": true, "--write-out": true,
}

const curlShort = "sSLIivfgGkm4"

func wordTexts(ws []word) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.text
	}
	return out
}

func program(prog string, texts []string, args []word, tier, cwd string) string {
	switch prog {
	case "git":
		return gitCmd(texts, tier, cwd)
	case "gh":
		return ghCmd(texts, tier)
	case "podman", "docker":
		return containerCmd(prog, texts, tier, cwd)
	case "systemctl":
		return systemctlCmd(texts, tier)
	case "journalctl":
		if hasPrefixAny(texts, "--vacuum", "--rotate", "--flush", "--sync") {
			return "?"
		}
		return tier
	case "mkdir", "touch":
		return mkdirTouch(texts, cwd)
	case "cp", "mv", "ln", "install":
		return copyMove(prog, texts, cwd)
	case "chmod":
		return chmodCmd(texts, cwd)
	case "rm":
		return rmCmd(texts, args, cwd)
	case "python3", "python":
		return pythonCmd(texts, tier, cwd)
	}
	if buildAny[prog] || prog == "go" || prog == "cargo" || prog == "npm" || prog == "pnpm" || prog == "yarn" || prog == "make" {
		return buildCmd(prog, texts, tier, cwd)
	}
	if prog == "curl" || prog == "wget" {
		return curlCmd(prog, texts, tier)
	}
	return "?"
}

func gitCmd(texts []string, tier, cwd string) string {
	if len(texts) == 0 || strings.HasPrefix(texts[0], "-") {
		return "?"
	}
	sub, rest := texts[0], texts[1:]
	for _, t := range rest {
		if t == "-c" || t == "-O" || strings.HasPrefix(t, "-c") && len(t) > 2 && t[2] == '=' || prefixOf(t, gitBadLong) || strings.HasPrefix(t, "--output") {
			return "?"
		}
		if strings.HasPrefix(t, "-") && !strings.HasPrefix(t, "--") && strings.ContainsRune(t[1:], 'O') {
			return "?"
		}
	}
	var pos []string
	for _, t := range rest {
		if !strings.HasPrefix(t, "-") {
			pos = append(pos, t)
		}
	}
	for _, t := range rest {
		if t == "--no-index" {
			return "?"
		}
	}
	for _, t := range pos {
		if (strings.HasPrefix(t, "/") || hasComponent(t, "..")) && !localPath(t, cwd, true) {
			return "?"
		}
	}
	if gitRO[sub] {
		return tier
	}
	switch sub {
	case "branch":
		if len(pos) == 0 && !hasAny(rest, "-d", "-D", "-m", "-M", "-c", "-C", "--delete", "--move", "--copy", "-f", "--force", "-u", "--set-upstream-to") {
			return tier
		}
		return "?"
	case "remote":
		if (len(pos) == 0 || pos[0] == "-v" || pos[0] == "show" || pos[0] == "get-url") && !hasAny(pos, "add", "remove", "rm", "rename", "set-url", "prune") {
			return tier
		}
		return "?"
	case "config":
		if hasAny(rest, "--get", "--list", "-l", "--get-all") && !hasAny(rest, "--global", "--system", "--file", "-f", "--add", "--unset") {
			return tier
		}
		return "?"
	case "stash":
		if len(pos) == 0 || pos[0] == "list" || pos[0] == "show" || pos[0] == "push" || pos[0] == "save" {
			return "T0b"
		}
		return "?"
	case "worktree":
		if len(pos) > 0 && pos[0] == "list" {
			return tier
		}
		return "?"
	case "tag":
		if len(pos) == 0 && len(rest) == 0 {
			return tier
		}
		return "?"
	case "add":
		if hasAny(rest, "-f", "--force") {
			return "?"
		}
		for _, p := range pos {
			if p != "." && !localPath(p, cwd, true) {
				return "?"
			}
		}
		return "T0b"
	case "commit":
		for _, t := range rest {
			if t == "--amend" || t == "--no-verify" || t == "-n" || t == "--no-gpg-sign" || t == "-S" || t == "--gpg-sign" || strings.HasPrefix(t, "--amend") {
				return "?"
			}
		}
		return "T0b"
	case "switch", "fetch":
		for _, t := range rest {
			if t == "--force" || t == "-f" || t == "-C" || t == "--force-create" || t == "--discard-changes" || strings.HasPrefix(t, "--force") {
				return "?"
			}
		}
		return "T0b"
	case "mv":
		if len(pos) >= 2 && allLocal(pos, cwd) {
			return "T0b"
		}
		return "?"
	case "checkout":
		if len(rest) >= 2 && (rest[0] == "-b" || rest[0] == "-B") && !hasAny(rest, "--") {
			return "T0b"
		}
		return "?"
	case "apply":
		if hasAny(rest, "--check", "--stat", "--numstat", "--summary") {
			return tier
		}
		return "?"
	}
	return "?"
}

func hasComponent(p, c string) bool {
	for _, x := range strings.Split(p, "/") {
		if x == c {
			return true
		}
	}
	return false
}

func ghCmd(texts []string, tier string) string {
	if len(texts) >= 2 && !strings.HasPrefix(texts[0], "-") && ghRead[texts[0]][texts[1]] {
		if hasAny(texts, "--web", "-w") {
			return "?"
		}
		return tier
	}
	if len(texts) > 0 && texts[0] == "api" {
		flat := strings.Join(texts[1:], " ")
		if ghMut.Match(flat) && !ghGet.Match(flat) {
			return "?"
		}
		return tier
	}
	return "?"
}

func containerCmd(prog string, texts []string, tier, cwd string) string {
	sub := ""
	if len(texts) > 0 && !strings.HasPrefix(texts[0], "-") {
		sub = texts[0]
	}
	if sub == "" {
		return "?"
	}
	ro := map[string]bool{"ps": true, "images": true, "logs": true, "version": true, "info": true, "top": true, "port": true, "stats": true, "diff": true, "history": true, "events": true}
	if ro[sub] {
		for _, t := range texts {
			if strings.HasPrefix(t, "--") && prefixOf(t, badPrefix) {
				return "?"
			}
		}
		return tier
	}
	if sub == "run" {
		if tier != "?" && containerRunOK(texts[1:], cwd) {
			return "T0b"
		}
		return "?"
	}
	return "?"
}

func systemctlCmd(texts []string, tier string) string {
	sub := ""
	for _, t := range texts {
		if !strings.HasPrefix(t, "-") {
			sub = t
			break
		}
	}
	long := sub == "status" || sub == "is-active" || sub == "is-enabled" || sub == "list-units" || sub == "list-unit-files" || sub == "list-timers"
	short := sub == "status" || sub == "is-active" || sub == "is-enabled"
	if long && hasAny(texts, "--user") || short {
		return tier
	}
	return "?"
}

func mkdirTouch(texts []string, cwd string) string {
	var paths []string
	for _, t := range texts {
		if strings.HasPrefix(t, "-") {
			if t != "-p" && t != "-v" && t != "--parents" && t != "--verbose" && t != "-m" {
				return "?"
			}
			continue
		}
		paths = append(paths, t)
	}
	if len(paths) == 0 || !allLocalExec(paths, cwd) {
		return "?"
	}
	return "T0b"
}

func copyMove(prog string, texts []string, cwd string) string {
	var opts, paths []string
	for _, t := range texts {
		if strings.HasPrefix(t, "-") {
			opts = append(opts, t)
		} else {
			paths = append(paths, t)
		}
	}
	for _, o := range opts {
		if !cpOpt.Match(o) {
			return "?"
		}
	}
	if prog == "ln" {
		ok := false
		for _, o := range opts {
			if lnSym.Match(o) {
				ok = true
			}
		}
		if !ok {
			return "?"
		}
	}
	if len(paths) >= 2 && allLocalExec(paths, cwd) {
		return "T0b"
	}
	return "?"
}

func chmodCmd(texts []string, cwd string) string {
	var modes, paths []string
	for _, t := range texts {
		if chmodMode.Match(t) {
			modes = append(modes, t)
		}
	}
	for _, t := range texts {
		if !strings.HasPrefix(t, "-") && !strSliceHas(modes, t) {
			paths = append(paths, t)
		}
	}
	if len(modes) == 0 {
		return "?"
	}
	for _, t := range texts {
		if strings.HasPrefix(t, "-") {
			return "?"
		}
	}
	for _, m := range modes {
		if m == "777" || m == "0777" || m == "a+rwx" || m == "o+w" || m == "a+w" || m == "+w" || m == "666" || strings.ContainsRune(m, 's') || strings.ContainsRune(m, 't') {
			return "?"
		}
	}
	if len(paths) > 0 && allLocalExec(paths, cwd) {
		return "T0b"
	}
	return "?"
}

func rmCmd(texts []string, args []word, cwd string) string {
	var flags, paths []string
	for _, t := range texts {
		if strings.HasPrefix(t, "-") {
			flags = append(flags, t)
		} else {
			paths = append(paths, t)
		}
	}
	if len(paths) == 0 {
		return "?"
	}
	for _, f := range flags {
		if !rmFlag.Match(f) {
			return "?"
		}
	}
	rec := false
	for _, f := range flags {
		if rmRec.Match(f) {
			rec = true
		}
	}
	for _, a := range args {
		if a.glob && !strings.HasPrefix(a.text, "/tmp/") && !strings.HasPrefix(a.text, "/dev/shm/") && !strings.HasPrefix(a.text, "/var/tmp/") {
			return "?"
		}
	}
	for _, p := range paths {
		q, ok := norm(p, cwd)
		if !ok || sensitive.Match(q) || execCfg.Match(q) {
			return "?"
		}
		root := strings.TrimRight(cwd, "/")
		if q == "/" || q == "/tmp" || q == "/dev/shm" || q == "/var/tmp" || q == root || homeLike(q) {
			return "?"
		}
		inScratch := false
		for _, s := range scratch {
			if strings.HasPrefix(q, s+"/") {
				inScratch = true
				break
			}
		}
		if inScratch && (len(strings.Split(q, "/")) >= 4 || !rec) {
			continue
		}
		if localPath(p, cwd, false) {
			if buildDirs[pycompat.Basename(q)] {
				continue
			}
			if !rec {
				continue
			}
		}
		return "?"
	}
	return "T0b"
}

func pythonCmd(texts []string, tier, cwd string) string {
	if len(texts) > 0 && texts[0] == "-m" && len(texts) > 1 && (texts[1] == "pytest" || texts[1] == "unittest" || texts[1] == "py_compile") {
		for _, t := range texts[2:] {
			if strings.HasPrefix(t, "/") && !localPath(t, cwd, true) {
				return "?"
			}
		}
		return "T0b"
	}
	if len(texts) == 1 && (texts[0] == "--version" || texts[0] == "-V") {
		return tier
	}
	return "?"
}

func buildCmd(prog string, texts []string, tier, cwd string) string {
	sub := ""
	for _, t := range texts {
		if !strings.HasPrefix(t, "-") {
			sub = t
			break
		}
	}
	if prog == "go" && sub == "env" && hasAny(texts, "-w", "-u") {
		return "?"
	}
	if (prog == "npm" || prog == "pnpm" || prog == "yarn") && (sub == "run" || sub == "exec" || sub == "x" || sub == "dlx") {
		if !(sub == "run" && len(texts) >= 2 && strSliceHas([]string{"test", "build", "lint", "typecheck", "check", "format", "fmt", "dev-check"}, texts[1])) {
			return "?"
		}
	}
	if prog == "make" {
		joined := strings.Join(texts, " ")
		for _, x := range []string{"deploy", "release", "publish", "install", "prod", "push", "wipe", "clean-all"} {
			if strings.Contains(joined, x) {
				return "?"
			}
		}
		for _, t := range texts {
			if strings.HasPrefix(t, "-") && t != "-j" && t != "-k" && t != "-n" && t != "-s" && !jobFlag.Match(t) {
				return "?"
			}
		}
		if tier == "?" {
			return "?"
		}
		return "T0b"
	}
	writers := map[string]bool{"gofmt": true, "goimports": true, "prettier": true, "black": true, "ruff": true}
	for _, t := range texts {
		if !strings.HasPrefix(t, "-") {
			continue
		}
		if writers[prog] && (t == "-w" || t == "--write") {
			continue
		}
		name, _, _ := strings.Cut(strings.TrimLeft(t, "-"), "=")
		if outFlag.Match(name) {
			return "?"
		}
	}
	for _, t := range texts {
		if strings.HasPrefix(t, "/") && !localPath(t, cwd, true) {
			return "?"
		}
	}
	if buildAny[prog] {
		if tier == "?" {
			return "?"
		}
		return "T0b"
	}
	if buildPairs[[2]string{prog, sub}] {
		if tier == "?" {
			return "?"
		}
		return "T0b"
	}
	return "?"
}

func curlCmd(prog string, texts []string, tier string) string {
	if prog == "wget" {
		return "?"
	}
	for _, t := range texts {
		if strings.HasPrefix(t, "@") || strings.Contains(t, "=@") {
			return "?"
		}
	}
	var urls []string
	for i := 0; i < len(texts); i++ {
		t := texts[i]
		if strings.HasPrefix(t, "--") {
			name, _, _ := strings.Cut(t, "=")
			if !curlLong[name] {
				return "?"
			}
			if curlValue[name] && !strings.Contains(t, "=") {
				i++
			}
		} else if strings.HasPrefix(t, "-") && len(t) > 1 {
			rs := []rune(t)
			for j := 1; j < len(rs); j++ {
				ch := rs[j]
				if ch == 'm' || ch == 'A' || ch == 'w' || ch == 'H' {
					if ch == 'H' && i+1 < len(texts) && strings.HasPrefix(strings.TrimLeft(texts[i+1], " \t"), "@") {
						return "?"
					}
					if j == len(rs)-1 {
						i++
					}
					break
				}
				if !strings.ContainsRune(curlShort, ch) {
					return "?"
				}
			}
		} else {
			urls = append(urls, t)
		}
	}
	if len(urls) == 0 {
		return "?"
	}
	for _, u := range urls {
		if !curlURL.Match(u) {
			return "?"
		}
	}
	return tier
}

func sedCmd(texts []string, tier, cwd string) string {
	var opts, pos []string
	for _, t := range texts {
		if strings.HasPrefix(t, "-") {
			opts = append(opts, t)
		} else {
			pos = append(pos, t)
		}
	}
	inplace := false
	for _, o := range opts {
		switch o {
		case "-n", "-E", "-r", "--quiet", "--silent", "--regexp-extended":
			continue
		case "-i", "--in-place":
			inplace = true
			continue
		}
		if sedOpt.Match(o) {
			continue
		}
		return "?"
	}
	if len(pos) == 0 || !sedScriptOK(pos[0]) {
		return "?"
	}
	files := pos[1:]
	if inplace {
		if tier == "?" || len(files) == 0 || !allLocalExec(files, cwd) {
			return "?"
		}
		return "T0b"
	}
	return tier
}

func sedScriptOK(s string) bool {
	if sedAddr.Match(s) || sedNumQ.Match(s) {
		return true
	}
	if len(s) < 2 || s[0] != 's' {
		return false
	}
	d, _ := nextRune(s[1:])
	if pycompat.IsAlnum(d) || strings.ContainsRune("\\\n\" ;{}", d) {
		return false
	}
	body := s[1+len(string(d)):]
	var parts []string
	cur := ""
	rs := []rune(body)
	for k := 0; k < len(rs); k++ {
		ch := rs[k]
		if ch == '\\' && k+1 < len(rs) {
			cur += string(rs[k : k+2])
			k++
			continue
		}
		if ch == d {
			parts = append(parts, cur)
			cur = ""
			continue
		}
		cur += string(ch)
	}
	parts = append(parts, cur)
	return len(parts) == 3 && sedFlags.Match(parts[2])
}

func nextRune(s string) (rune, int) {
	for _, r := range s {
		return r, 1
	}
	return 0, 0
}

func allLocal(paths []string, cwd string) bool {
	for _, p := range paths {
		if !localPath(p, cwd, true) {
			return false
		}
	}
	return true
}

func allLocalExec(paths []string, cwd string) bool {
	for _, p := range paths {
		q, ok := norm(p, cwd)
		if !localPath(p, cwd, true) || !ok || execCfg.Match(q) {
			return false
		}
	}
	return true
}

func prefixOf(arg string, dangerous []string) bool {
	name, _, _ := strings.Cut(arg, "=")
	if !strings.HasPrefix(name, "--") || len([]rune(name)) < 3 {
		return false
	}
	for _, d := range dangerous {
		if strings.HasPrefix(d, name) {
			return true
		}
	}
	return false
}

func hasAny(ss []string, want ...string) bool {
	for _, s := range ss {
		for _, w := range want {
			if s == w {
				return true
			}
		}
	}
	return false
}

func hasPrefixAny(ss []string, prefs ...string) bool {
	for _, s := range ss {
		for _, p := range prefs {
			if strings.HasPrefix(s, p) {
				return true
			}
		}
	}
	return false
}
