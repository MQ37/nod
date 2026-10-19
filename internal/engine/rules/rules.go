// Package rules ports the deterministic, severity-raising rules from rules.py.
package rules

import (
	"github.com/MQ37/nod/internal/engine/pycompat"
	"github.com/MQ37/nod/internal/engine/tiers"
	"path"
	"regexp"
	"strconv"
	"strings"
)

const (
	None  = 0
	Ask   = 1
	Block = 2
)

var AskIgnore = map[string]bool{"container operation": true, "installs software": true, "changes branch state or the working tree": true, "indirect execution or bulk delete": true, "container data deletion": true, "state-changing HTTP request": true, "connects to a remote host": true, "copies files to or from a remote host": true}
var home = `(?:~|\$\{?HOME\}?|/home/[^/\s'"]+|/root|/Users/[^/\s'"]+)`

type rule struct {
	re     *regexp.Regexp
	level  int
	reason string
}

func compileRules(src [][3]string, insensitive bool) []rule {
	out := make([]rule, 0, len(src))
	for _, x := range src {
		p := x[0]
		p = strings.ReplaceAll(p, `(?<![\w.])\.env(?:\.(?!example|sample|template|dist)[\w-]+)?(?![\w.])`, `(^|[^\w.])\.env(?:\.[\w-]+)?($|[^\w.])`)
		p = strings.ReplaceAll(p, `\bdd\b[^\n]*\bof=/dev/(?!null|zero|stdout|stderr)`, `\bdd\b[^\n]*\bof=/dev/`)
		p = strings.ReplaceAll(p, `\bgit\s+apply\b(?![^\n]*--(?:check|stat|numstat|summary))`, `\bgit\s+apply\b`)
		p = strings.ReplaceAll(p, `(?<![\w.])`, `(?:^|[^\w.])`)
		p = strings.ReplaceAll(p, `(?=\s*(?:-\s*)?(?:$|[|;&)\n]))`, `(?:\s*(?:-\s*)?(?:$|[|;&)\n]))`)
		p = strings.ReplaceAll(p, `(?![^\n]*--(?:check|stat|numstat|summary))`, `(?:[^\n]*--(?:check|stat|numstat|summary))`)
		p = strings.ReplaceAll(p, `(?!/(?:null|zero|stdout|stderr))`, `(?:/(?:null|zero|stdout|stderr))`)
		p = strings.ReplaceAll(p, `(?!/tmp)`, `(?:/tmp)`)
		p = strings.ReplaceAll(p, `(?![\w.])`, `(?:$|[^\w.])`)
		p = strings.ReplaceAll(p, `(?!example|sample|template|dist)`, `(?:[^e]|e[^x]|ex[^a]|exa[^m]|exam[^p]|examp[^l]|exampl[^e])`)
		if insensitive {
			p = "(?i)" + p
		}
		re, e := regexp.Compile(pycompat.Translate(p))
		if e != nil {
			continue
		}
		out = append(out, rule{re, map[string]int{"ASK": Ask, "BLOCK": Block}[x[1]], x[2]})
	}
	return out
}

var commandRules = compileRules(commandPatterns(), true)
var pathRules = compileRules(pathPatterns(), true)
var urlRules = compileRules([][3]string{{`169\.254\.169\.254|metadata\.google\.internal|100\.100\.100\.200|^file:|^(?:ftp|gopher|ldap|dict):`, `BLOCK`, `internal or non-http URL`}}, true)

func commandPatterns() [][3]string {
	return [][3]string{
		{`\b(?:curl|wget|fetch)\b[^|;&\n]*\|\s*(?:sudo\s+(?:-\w+\s+)*)?(?:ba|z|da|k|fi)?sh\b`, `BLOCK`, `pipes a download into a shell`},
		{`\b(?:curl|wget)\b[^|;&\n]*\|\s*(?:sudo\s+)?(?:python[\d.]*|perl|ruby|node|php)(?:\s*(?:-\s*)?(?:$|[|;&)\n]))`, `BLOCK`, `pipes a download into an interpreter`},
		{`(?:\bsh|\bbash|\bzsh|\beval|\bsource|\b\.)\s+(?:-c\s+)?["']?(?:\$\(|<\(|\x60)\s*(?:curl|wget)\b`, `BLOCK`, `runs code fetched from the network`},
		{`(?:\bbash|\bsh|\bzsh|\bsource)\s+<\(\s*(?:curl|wget)\b`, `BLOCK`, `runs code fetched from the network`},
		{`/dev/(?:tcp|udp)/`, `BLOCK`, `reverse-shell style socket`}, {`\b(?:nc|ncat|netcat)\b[^|;&\n]*\s-[a-z]*e\b`, `BLOCK`, `netcat with -e`}, {`\bsocat\b[^|;&\n]*\bexec:`, `BLOCK`, `socat exec`},
		{`\bbase64\s+(?:-d|--decode)\b[^|;&\n]*\|\s*(?:sudo\s+)?(?:ba|z)?sh\b`, `BLOCK`, `decodes a payload into a shell`}, {`(?:os\.system|subprocess\.\w+|child_process|exec\()[^\n]*(?:curl|wget)[^\n]*\|\s*(?:ba)?sh`, `BLOCK`, `script wrapper around curl|sh`},
		{`\|\s*(?:curl|wget|nc|ncat|netcat|socat)\b`, `BLOCK`, `pipes local data to a network tool`}, {`\b(?:curl|wget)\b[^\n]*(?:\s-d\s*@|--data(?:-binary|-raw|-urlencode)?[ =]@|\s-T\s|--upload-file|\s-F\s*\S*=?@)`, `BLOCK`, `uploads a local file`}, {`\b(?:env|printenv|export\s+-p)\b[^|;&\n]*\|\s*(?:curl|wget|nc)`, `BLOCK`, `sends the environment out`}, {`169\.254\.169\.254|metadata\.google\.internal|100\.100\.100\.200|fd00:ec2::254`, `BLOCK`, `cloud metadata endpoint (credentials)`},
		{`\b(?:scp|rsync|sftp)\b`, `ASK`, `copies files to or from a remote host`}, {`\b(?:curl|wget)\b[^\n]*(?:\s-X\s*(?:POST|PUT|PATCH|DELETE)\b|\s-d\b|--data|--request\s+(?:POST|PUT|PATCH|DELETE))`, `ASK`, `state-changing HTTP request`},
		{home + `/\.(?:ssh|aws|gnupg|gpg|kube|netrc|git-credentials|npmrc|pypirc|pgpass|my\.cnf|vault-token|terraform\.d|azure|boto)\b`, `BLOCK`, `touches credential files`}, {home + `/\.config/(?:gh|gcloud|rclone|op|doctl|hub|git/credentials|pip/pip\.conf)\b`, `BLOCK`, `touches credential files`}, {home + `/\.docker/config\.json`, `BLOCK`, `touches credential files`}, {`/etc/(?:shadow|gshadow|sudoers|ssh/ssh_host)`, `BLOCK`, `touches system credential files`},
		{`\bid_(?:rsa|ed25519|ecdsa|dsa)\b|\bsecring\.gpg\b|\.kdbx\b|\bcredentials\.json\b|\bservice[-_]account[^\s]*\.json\b`, `BLOCK`, `touches key or credential files`}, {`\bfind\s+/[^\n]*(?:-name\s+['"]?\*\.(?:pem|key|p12|pfx|kdbx)|id_rsa|\.env\b|credentials)`, `BLOCK`, `sweeps the filesystem for secrets`}, {`\b(?:gh\s+auth\s+token|security\s+find-(?:generic|internet)-password|secret-tool\s+lookup|op\s+(?:read|item\s+get)|lockbox\s+(?:get|show|export))\b`, `BLOCK`, `reads a stored secret`}, {`(?<![\w.])\.env(?:\.(?!example|sample|template|dist)[\w-]+)?(?![\w.])`, `ASK`, `reads or writes a .env file`},
		{`\bdd\b[^\n]*\bof=/dev/(?!null|zero|stdout|stderr)`, `BLOCK`, `writes to a raw device`}, {`\b(?:mkfs(?:\.\w+)?|wipefs|blkdiscard)\b|\b(?:fdisk|parted|sgdisk)\b[^\n]*(?:/dev/|-w\b)|\bshred\b[^\n]*/dev/`, `BLOCK`, `formats or wipes a disk`}, {`:\(\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:`, `BLOCK`, `fork bomb`}, {`>\s*/dev/(?:sd[a-z]|nvme\d|vd[a-z]|disk\d)`, `BLOCK`, `overwrites a raw device`}, {`\b(?:drop\s+(?:table|database|schema|index)|truncate\s+(?:table\s+)?\w)`, `BLOCK`, `destructive SQL`}, {`\bdelete\s+from\s+[\w."\x60]+\s*(?:;|"|'|$)`, `BLOCK`, `DELETE without WHERE`},
		{`\bkubectl\s+delete\s+(?:ns|namespaces?|nodes?|pv|persistentvolumes?|crd|clusterrole\w*)\b|\bkubectl\s+delete\b[^\n]*--all\b`, `BLOCK`, `mass Kubernetes delete`}, {`\bterraform\s+(?:destroy|apply\s+[^\n]*-auto-approve)|\bpulumi\s+destroy\b`, `BLOCK`, `infrastructure destroy / unreviewed apply`}, {`\baws\s+s3\s+(?:rb|rm\s+[^\n]*--recursive)|\baws\s+\w+\s+(?:delete|terminate|remove)-[\w-]+|\bgcloud\s+[^\n]*\bdelete\b|\baz\s+[^\n]*\bdelete\b`, `BLOCK`, `cloud resource deletion`}, {`\bgh\s+repo\s+(?:delete|archive)\b|\bgh\s+api\b[^\n]*-X\s*DELETE|\bgit\s+push\b[^\n]*--delete\b`, `BLOCK`, `deletes remote data`},
		{`\b(?:npm|yarn|pnpm)\s+publish\b|\bcargo\s+publish\b|\btwine\s+upload\b|\bgem\s+push\b|\bdocker\s+push\b|\bpodman\s+push\b|\bgo\s+mod\s+publish\b`, `BLOCK`, `publishes a package or image`}, {`\bgit\s+clean\b[^\n]*-\w*[fx]\w*[^\n]*|\bgit\s+clean\b[^\n]*-\w*x`, `ASK`, `git clean discards untracked files`}, {`\bgit\s+clean\b[^\n]*-\w*f\w*x|\bgit\s+clean\b[^\n]*-\w*x\w*f`, `BLOCK`, `git clean -fdx wipes ignored and untracked files`}, {`\bgit\s+reset\s+--hard\b|\bgit\s+(?:checkout|restore)\s+(?:--\s+)?\.(?:\s|$)|\bgit\s+branch\s+-D\b|\bgit\s+stash\s+(?:drop|clear)\b|\bgit\s+filter-(?:branch|repo)\b`, `ASK`, `discards local work or rewrites history`}, {`\b(?:docker|podman)\s+(?:system\s+prune|volume\s+(?:rm|prune)|rm\s+-f\s+\$\()`, `ASK`, `container data deletion`},
		{`\bchmod\s+(?:-\w+\s+)*(?:[0-7]?[0-7][0-7][2367]\b|[ugoa]*\+[rwx]*s\b|[ugoa]*=[rwx]*s\b|(?:-R\s+)?[0-7]*[4-7][0-7]{3}\b)`, `BLOCK`, `setuid/world-writable permission change`}, {`\bchmod\s+(?:-\w+\s+)*\d*777\b|\bchmod\s+-R\b[^\n]*\s/(?:\s|$|etc|usr|var|home|bin)`, `BLOCK`, `sweeping permission change`}, {`\bchown\s+-R\b[^\n]*\s/(?:\s|$|etc|usr|var|bin|home)`, `BLOCK`, `sweeping ownership change`}, {`\b(?:usermod|adduser|useradd|gpasswd)\b[^\n]*\b(?:sudo|wheel|root|docker|adm)\b`, `BLOCK`, `grants a user elevated groups`}, {`authorized_keys|/etc/(?:cron|systemd|rc\.local|ld\.so\.preload|profile\.d)|/etc/passwd|\bvisudo\b|/etc/sudoers`, `BLOCK`, `persistence or account tampering`}, {`\bcrontab\b[^\n]*(?:-\s*$|\|\s*crontab|-r\b)|\|\s*crontab\b`, `BLOCK`, `installs or wipes cron jobs`}, {`\bsudo\s+(?:-\w+\s+)*(?:rm|dd|mkfs|chmod|chown|mv|tee|sed|visudo|passwd|userdel|rmmod|insmod)\b`, `BLOCK`, `root-level destructive command`}, {`\bhistory\s+-c\b|\bunset\s+HISTFILE\b|\bshred\b[^\n]*(?:history|\.log)|rm\b[^\n]*(?:\.bash_history|\.zsh_history|/var/log)|journalctl\s+--vacuum`, `BLOCK`, `erases traces`}, {`\bsetenforce\s+0|\bufw\s+disable|\biptables\s+-F|\bsystemctl\s+(?:stop|disable|mask)\s+(?:firewalld|ufw|apparmor|auditd|fail2ban)`, `BLOCK`, `disables a security control`}, {`\b(?:sudo|su|doas|pkexec)\b`, `ASK`, `runs with elevated privileges`},
		{`\bgit\s+push\b`, `ASK`, `pushes to a remote`}, {`\bgit\s+(?:pull|merge|rebase|cherry-pick|am)\b|\bgit\s+apply\b(?![^\n]*--(?:check|stat|numstat|summary))`, `ASK`, `changes branch state or the working tree`}, {`\b(?:ssh|mosh|telnet|ftp)\b\s`, `ASK`, `connects to a remote host`}, {`\b(?:pip3?|pipx|uv|poetry|npm|npx|yarn|pnpm|bun|cargo|gem|go\s+(?:get|install)|apt(?:-get)?|dnf|yum|pacman|brew|snap|flatpak|conda)\s+(?:install|add|i|update|upgrade|ci|get|create)\b|\bnpm\s+ci\b|\bnpx\b`, `ASK`, `installs software`}, {`\b(?:docker|podman)\s+(?:pull|login|run|compose|build|exec)\b|\bdocker-compose\b`, `ASK`, `container operation`}, {`\b(?:kubectl|helm|terraform|pulumi|aws|gcloud|az|doctl|flyctl|heroku|vercel|netlify|wrangler)\b`, `ASK`, `cloud or cluster tooling`}, {`\b(?:FLUSHALL|FLUSHDB|dropdb|dropDatabase|drop_database)\b|\bredis-cli\b[^\n;|&]*\bFLUSH`, `ASK`, `destructive SQL or datastore wipe`}, {`\b(?:psql|mysql|mariadb|mongosh?|redis-cli|sqlite3|clickhouse-client|cqlsh)\b`, `ASK`, `database client`},
		{`\bgh\s+(?:pr\s+(?:create|edit|merge|close|reopen|comment|review|ready|lock|unlock|checkout)|issue\s+(?:create|edit|close|reopen|comment|delete|transfer|pin|unpin|lock|unlock)|release\s+(?:create|edit|delete|upload)|workflow\s+(?:run|enable|disable)|secret\b|repo\s+(?:create|edit|fork|rename|delete|archive|unarchive|sync|clone)|label\s+(?:create|edit|delete|clone)|run\s+(?:rerun|cancel|delete)|api\b[^\n]*(?:-X\s*(?:POST|PUT|PATCH|DELETE)|--method[= ](?:POST|PUT|PATCH|DELETE)|\s-[fF]\s|--field|--raw-field|--input))`, `ASK`, `GitHub write operation`}, {`\bsystemctl\b|\bservice\s+\w+\s+(?:start|stop|restart)|\bkill(?:all)?\b|\bpkill\b|\bcrontab\b|\bmount\b|\bumount\b|\bmodprobe\b|\binsmod\b|\bsysctl\b`, `ASK`, `system or process control`}, {`\b(?:eval|xargs\s+(?:-\w+\s+)*rm|find\b[^\n]*(?:-delete|-exec\s+rm))\b`, `ASK`, `indirect execution or bulk delete`},
		{`(?:>>?|\btee\b(?:\s+-a)?)\s*` + home + `/(?:\.(?:bashrc|bash_profile|zshrc|profile|zprofile|config/|local/bin/|gitconfig)|bin/)`, `ASK`, `edits shell startup files or personal bin`}, {`\bgit\s+config\s+--global\b|\bgit\s+config\b[^\n]*core\.hooksPath`, `ASK`, `changes global git behaviour`}, {`(?:toolguard|nod)-gate/|(?:toolguard|nod)/(?:rules|guard|server)\.py|(?:toolguard|nod)/pi/(?:policy|extension)|\.hermes/plugins/`, `ASK`, `edits the guard's own code or plugin`}, {`\.git/hooks/|\.claude/(?:settings|hooks)|\.vscode/tasks\.json|\.husky/`, `ASK`, `edits something that later runs code or permissions`}, {`\bcurl\b[^\n]*(?:\s-k\b|--insecure)|\bgit\b[^\n]*--no-verify|\b(?:npm|pip)[^\n]*--(?:no-verify|trusted-host)`, `ASK`, `turns off a safety check`}, {`\b(?:cursor-agent|claude|codex|gemini|opencode|aider|goose|omp|hermes|amp|qwen|kimi)\b[^|;&\n]*(?:--dangerously[\w-]*|--yolo|--trust\b|--sandbox[= ]disabled|--approve-mcps|--full-auto|--bypass[\w-]*|--no-sandbox|--auto-approve|--allow-all[\w-]*)`, `ASK`, `launches an agent with its safety checks off`}, {`\$'\\x[0-9a-f]{2}|\$\{IFS\}|\\x[0-9a-f]{2}\\x[0-9a-f]{2}|\bprintf\s+['"]\\[0-7x]`, `ASK`, `obfuscated command text`},
	}
}

func pathPatterns() [][3]string {
	return [][3]string{{home + `/\.(?:ssh|aws|gnupg|gpg|kube|netrc|git-credentials|pgpass)\b|` + home + `/\.config/(?:gh|gcloud|rclone|op)\b|/etc/(?:shadow|gshadow|sudoers)`, `BLOCK`, `credential or key path`}, {`(?:^|/)id_(?:rsa|ed25519|ecdsa|dsa)\b|\.(?:pem|p12|pfx|kdbx)\b|(?:^|/)credentials\.json\b|(?:^|/)\.netrc\b`, `BLOCK`, `credential or key file`}, {`^/(?:etc|usr|bin|sbin|boot|lib|lib64|sys|proc|root)(?:/|$)|^/dev(?:/|$)`, `BLOCK`, `system path`}, {home + `/\.(?:bashrc|bash_profile|zshrc|profile|zprofile|gitconfig|config|local/bin)\b|` + home + `/bin/`, `ASK`, `personal shell/config path`}, {`\.git/hooks/|\.claude/(?:settings|hooks)|\.husky/|\.vscode/tasks\.json|authorized_keys`, `ASK`, `path that later executes code or grants access`}, {`(^|[^\w.])\.env(\.[\w-]+)?(\b|$)`, `ASK`, `.env file`}}
}

var heredocRE = regexp.MustCompile(`(?s)<<-?[ \t]*['"]?\w+['"]?[^\n]*\n.*?(?:\n[ \t]*\w+[ \t]*(?:\n|$)|$)`)
var interpreterRE = pycompat.MustCompile(`\b(?:ba|z|da|k)?sh\b|\bpython[\d.]*\b|\bnode\b|\bperl\b|\bruby\b|\bphp\b|\bpsql\b|\bmysql\b|\bsqlite3\b|\bssh\b|\bkubectl\b|\bdocker\b|\bpodman\b|\bsudo\b|\bsource\b|\beval\b|\bxargs\b|\bexec\b|\bsu\b|\bdoas\b|\bmongosh?\b|\bredis-cli\b|\blua\b|\bRscript\b|\bbc\b|\bawk\b|\bsed\b`, `i`)
var quoteText = pycompat.MustCompile(`'[^']*\s[^']*'|"[^"]*\s[^"]*"`, `s`)
var redirRE = pycompat.MustCompile(`(?:\d*>>?|&>>?|<)\s*\S+`, "")
var envFileRE = regexp.MustCompile(`(?i)(?:^|[^\w.])\.env(?:\.([\w-]+))?(?:$|[^\w.])`)

func isEnvFile(s string) bool {
	for _, m := range envFileRE.FindAllStringSubmatch(s, -1) {
		ext := ""
		if len(m) > 1 {
			ext = strings.ToLower(m[1])
		}
		if ext != "example" && ext != "sample" && ext != "template" && ext != "dist" {
			return true
		}
	}
	return false
}

var ddOutRE = regexp.MustCompile(`(?i)\bdd\b[^\n]*\bof=/dev/([^\s;&|]+)`)

func hasRawDeviceWrite(s string) bool {
	for _, m := range ddOutRE.FindAllStringSubmatch(s, -1) {
		if len(m) > 1 && m[1] != "null" && m[1] != "zero" && m[1] != "stdout" && m[1] != "stderr" {
			return true
		}
	}
	return false
}

var applySafeRE = regexp.MustCompile(`(?i)\bgit\s+apply\b[^\n;&|]*(?:^|\s)--(?:check|stat|numstat|summary)(?:\s|$)`)

func stripDataHeredocs(s string) string {
	return heredocRE.ReplaceAllStringFunc(s, func(m string) string {
		n := strings.IndexByte(m, '\n')
		if n < 0 {
			return m
		}
		first := m[:n+1]
		at := strings.Index(s, m)
		line := strings.LastIndex(s[:at], "\n") + 1
		owner := s[line:at] + first
		if interpreterRE.Match(owner) {
			return m
		}
		return first
	})
}

func splitSimple(s string) []string {
	var segs []string
	var b strings.Builder
	q := rune(0)
	r := []rune(s)
	for i := 0; i < len(r); i++ {
		c := r[i]
		if q == '\'' {
			b.WriteRune(c)
			if c == '\'' {
				q = 0
			}
			continue
		}
		if q == '"' {
			if c == '\\' && i+1 < len(r) {
				b.WriteRune(c)
				i++
				b.WriteRune(r[i])
				continue
			}
			if c == '`' || (c == '$' && i+1 < len(r) && r[i+1] == '(') {
				return nil
			}
			b.WriteRune(c)
			if c == '"' {
				q = 0
			}
			continue
		}
		if c == '\\' && i+1 < len(r) {
			b.WriteRune(c)
			i++
			b.WriteRune(r[i])
			continue
		}
		if c == '\'' || c == '"' {
			q = c
			b.WriteRune(c)
			continue
		}
		if c == '`' || (c == '$' && i+1 < len(r) && r[i+1] == '(') || (c == '<' || c == '>') && i+1 < len(r) && r[i+1] == '(' || (c == '<' && i+1 < len(r) && r[i+1] == '<') {
			return nil
		}
		if c == ';' || c == '\n' || c == '|' || c == '&' && i+1 < len(r) && r[i+1] == '&' {
			segs = append(segs, b.String())
			b.Reset()
			if c == '|' && i+1 < len(r) && r[i+1] == '|' || c == '&' && i+1 < len(r) && r[i+1] == '&' {
				i++
			}
			continue
		}
		if c == '&' && !(i > 0 && (r[i-1] == '>' || r[i-1] == '&')) && !(i+1 < len(r) && r[i+1] == '>') {
			segs = append(segs, b.String())
			b.Reset()
			continue
		}
		b.WriteRune(c)
	}
	if q != 0 {
		return nil
	}
	segs = append(segs, b.String())
	out := segs[:0]
	for _, x := range segs {
		if strings.TrimSpace(x) != "" {
			out = append(out, strings.TrimSpace(x))
		}
	}
	return out
}

func dequote(s string) string { return strings.NewReplacer("\\", "", "'", "", `"`, "").Replace(s) }

func segments(s string) [][]string {
	var out [][]string
	for _, seg := range regexp.MustCompile(`&&|\|\||;|\n|\|`).Split(s, -1) {
		t, e := pycompat.ShlexSplit(seg)
		if e != nil {
			t = strings.Fields(seg)
		}
		if len(t) > 0 {
			out = append(out, t)
		}
	}
	return out
}

func askView(s string) string {
	segs := splitSimple(s)
	if segs == nil {
		return s
	}
	out := make([]string, 0, len(segs))
	textProgs := set("echo printf")
	quoteProgs := set("grep egrep fgrep rg ack ag cat head tail less more wc sort uniq diff cmp jq tee tr cut column nl fold stat file ls tree find which type date true false test [")
	grepProgs := set("grep egrep fgrep rg ack ag")
	dataGit := set("commit log show diff add status blame grep tag branch stash describe shortlog ls-files rev-parse")
	for _, seg := range segs {
		ts, e := pycompat.ShlexSplit(seg)
		if e != nil {
			return s
		}
		i := 0
		for i < len(ts) && (regexp.MustCompile(`^[A-Za-z_]\w*=`).MatchString(ts[i]) || contains("time nohup nice command", ts[i])) {
			i++
		}
		if i < len(ts) && ts[i] == "timeout" && i+1 < len(ts) && regexp.MustCompile(`^\d+[smhd]?$`).MatchString(ts[i+1]) {
			i += 2
		}
		if i >= len(ts) {
			out = append(out, seg)
			continue
		}
		prog := path.Base(ts[i])
		sub := ""
		for _, a := range ts[i+1:] {
			if !strings.HasPrefix(a, "-") {
				sub = a
				break
			}
		}
		if textProgs[prog] {
			matches := redirRE.FindAllString(seg, -1)
			out = append(out, prog+" "+strings.Join(matches, " "))
			continue
		}
		if quoteProgs[prog] || (prog == "git" && dataGit[sub]) {
			masked := quoteText.ReplaceAllString(seg, "''")
			if grepProgs[prog] {
				words, e := pycompat.ShlexSplit(masked)
				if e != nil {
					return s
				}
				k := -1
				for j := 1; j < len(words); j++ {
					if !strings.HasPrefix(words[j], "-") {
						k = j
						break
					}
				}
				blocked := false
				for _, w := range words[1:] {
					if contains("-e -f --regexp --file", w) {
						blocked = true
					}
				}
				if k >= 0 && !blocked {
					words[k] = "_"
					masked = strings.Join(words, " ")
				}
			}
			out = append(out, masked)
			continue
		}
		out = append(out, seg)
	}
	return strings.Join(out, " ; ")
}

func set(s string) map[string]bool {
	m := map[string]bool{}
	for _, x := range strings.Fields(s) {
		m[x] = true
	}
	return m
}
func contains(s, x string) bool {
	for _, v := range strings.Fields(s) {
		if v == x {
			return true
		}
	}
	return false
}

func rmRules(cmd, cwd string) [][2]interface{} {
	var hits [][2]interface{}
	for _, tok := range segments(cmd) {
		i := 0
		for i < len(tok) && contains("sudo doas command builtin time nohup env", tok[i]) {
			i++
		}
		if i >= len(tok) || tok[i] != "rm" {
			continue
		}
		args := tok[i+1:]
		flags := ""
		for _, a := range args {
			if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") {
				flags += strings.TrimLeft(a, "-")
			}
		}
		rec := strings.Contains(strings.ToLower(flags), "r") || contains(strings.Join(args, " "), "--recursive")
		var targets []string
		for _, a := range args {
			if !strings.HasPrefix(a, "-") {
				targets = append(targets, a)
			}
		}
		if contains(strings.Join(args, " "), "--no-preserve-root") {
			hits = append(hits, [2]interface{}{Block, "rm --no-preserve-root"})
		}
		if !rec {
			continue
		}
		for _, raw := range targets {
			p := raw
			dynamic := strings.ContainsAny(raw, "$`{}*")
			if strings.HasPrefix(raw, "/") && !dynamic {
				p = path.Clean(raw)
			} else if !strings.HasPrefix(raw, "~") && !strings.HasPrefix(raw, "$") && strings.Contains(raw, "..") && cwd != "" && !dynamic {
				p = path.Clean(cwd + "/" + raw)
			}
			if strings.HasPrefix(p, "..") && (p == ".." || strings.HasPrefix(p, "../")) && !strings.HasPrefix(p, "../"+path.Base(cwd)+"/") {
				hits = append(hits, [2]interface{}{Block, "recursive delete outside the project: " + p})
				continue
			}
			if regexp.MustCompile(`^(?:/|/\*|~/?|~/\*|\$\{?HOME\}?/?|\$\{?HOME\}?/\*|\*|\.|\.\.|\./\*|/\.\*|\.\*)$`).MatchString(p) {
				hits = append(hits, [2]interface{}{Block, "recursive delete of " + p})
				continue
			}
			if !(strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") || strings.HasPrefix(p, "$HOME") || strings.HasPrefix(p, "${HOME}")) {
				continue
			}
			pp := p
			if strings.HasPrefix(p, "~") {
				pp = "/home/_u" + strings.TrimPrefix(p, "~")
			}
			if under(pp, cwd) || strings.HasPrefix(pp, "/tmp/") || strings.HasPrefix(pp, "/var/tmp/") || strings.HasPrefix(pp, "/dev/shm/") || strings.HasPrefix(pp, "/run/user/") {
				continue
			}
			depth := 0
			for _, x := range strings.Split(pp, "/") {
				if x != "" && x != "*" {
					depth++
				}
			}
			lvl := Ask
			if depth <= 2 || strings.HasPrefix(p, "/etc") || strings.HasPrefix(p, "/usr") || strings.HasPrefix(p, "/var") || strings.HasPrefix(p, "/bin") || strings.HasPrefix(p, "/boot") || strings.HasPrefix(p, "/lib") || strings.HasPrefix(p, "/opt") || strings.HasPrefix(p, "/home") || strings.HasPrefix(p, "/root") || strings.HasPrefix(p, "~") || strings.HasPrefix(p, "$HOME") || strings.HasPrefix(p, "${HOME}") {
				lvl = Block
			}
			hits = append(hits, [2]interface{}{lvl, "recursive delete outside the project: " + p})
		}
	}
	return hits
}
func under(p, cwd string) bool {
	return cwd != "" && (p == cwd || strings.HasPrefix(p, strings.TrimRight(cwd, "/")+"/"))
}

var sysWrite = regexp.MustCompile(`^(?:/etc|/usr|/bin|/sbin|/lib|/boot|/var(?:/[^/]+)?|/opt|/root|/sys|/proc|/dev(?:/[^/]+)?)(?:/|$)|^(?:~|\$\{?HOME\}?|/home/[^/]+|/Users/[^/]+)/(?:\.[^/]+|\.config|\.ssh|bin)(?:/|$)`)

func sysWritePath(p string) bool {
	if !sysWrite.MatchString(p) {
		return false
	}
	if strings.HasPrefix(p, "/var/tmp/") || p == "/var/tmp" {
		return false
	}
	if strings.HasPrefix(p, "/dev/") {
		for _, ok := range []string{"null", "stdout", "stderr", "shm", "tty"} {
			if p == "/dev/"+ok || strings.HasPrefix(p, "/dev/"+ok+"/") {
				return false
			}
		}
	}
	return true
}

var redirectScan = regexp.MustCompile(`(?:[0-9]*>>?|>\||&>>?|<)\s*([^\s;&|<>()]+)`)

func redirectRules(cmd string) [][2]interface{} {
	var h [][2]interface{}
	for _, loc := range redirectScan.FindAllStringSubmatchIndex(cmd, -1) {
		if len(loc) < 4 {
			continue
		}
		start, end := loc[2], loc[3]
		operator := cmd[loc[0]:start]
		if strings.HasPrefix(operator, "<") || strings.Contains(operator, "&") && strings.HasPrefix(operator, "&>") {
			continue
		}
		if start > 0 && (cmd[start-1] == '<' || cmd[start-1] == '&' || cmd[start-1] >= '0' && cmd[start-1] <= '9') {
			continue
		}
		t := strings.Trim(cmd[start:end], `'"`)
		p := t
		if strings.HasPrefix(t, "/") {
			p = path.Clean(t)
		}
		if sysWritePath(p) {
			h = append(h, [2]interface{}{Ask, "redirect writes to a system or dotfile path: " + t})
		}
	}
	for _, ts := range segments(cmd) {
		if len(ts) > 0 && ts[0] == "tee" {
			for _, a := range ts[1:] {
				if !strings.HasPrefix(a, "-") {
					p := a
					if strings.HasPrefix(a, "/") {
						p = path.Clean(a)
					}
					if sysWritePath(p) {
						h = append(h, [2]interface{}{Ask, "tee writes to a system or dotfile path: " + a})
					}
				}
			}
		}
	}
	return h
}

func specialRules(cmd, cwd string) [][2]interface{} {
	h := rmRules(cmd, cwd)
	h = append(h, gitPushRules(cmd)...)
	h = append(h, redirectRules(cmd)...)
	if pycompat.MustCompile(`nod-gate/|nod/(?:rules|guard|server)\.py|\.hermes/plugins/`, "").MatchString(cmd) && pycompat.MustCompile(`\.write\(|write_text|\bsed\s+-i|\btee\b|>>?\s*[\w./~$]|\bcp\s|\bmv\b|\brm\b`, "").MatchString(cmd) {
		h = append(h, [2]interface{}{Ask, "modifies the guard's own code or plugin"})
	}
	return h
}
func gitPushRules(cmd string) [][2]interface{} {
	var h [][2]interface{}
	for _, t := range segments(cmd) {
		if len(t) < 2 || t[0] != "git" {
			continue
		}
		i := 0
		for i < len(t) && t[i] != "push" {
			i++
		}
		if i >= len(t) {
			continue
		}
		args := t[i+1:]
		for _, a := range args {
			if a == "-f" || a == "--force" || regexp.MustCompile(`^-[a-zA-Z]*f[a-zA-Z]*$`).MatchString(a) {
				h = append(h, [2]interface{}{Block, "force push"})
				break
			} else if strings.HasPrefix(a, "--force-with-lease") {
				h = append(h, [2]interface{}{Ask, "force-with-lease push"})
				break
			} else if strings.HasPrefix(a, "+") && len(a) > 1 {
				h = append(h, [2]interface{}{Block, "force push via +refspec"})
				break
			}
		}
		if contains(strings.Join(args, " "), "--mirror") || contains(strings.Join(args, " "), "--prune") {
			h = append(h, [2]interface{}{Block, "mirror/prune push"})
		}
	}
	return h
}

// Evaluate returns the highest severity and ordered, de-duplicated reasons.
func Evaluate(tool, text, cwd string, opt Options) (int, []string) {
	if opt.AskIgnore == nil && opt.MaxLen == 0 && !opt.AskRules {
		opt = DefaultOptions()
	}
	if opt.AskIgnore == nil {
		opt.AskIgnore = AskIgnore
	}
	if opt.MaxLen <= 0 {
		opt.MaxLen = 32768
	}
	level := None
	reasons := []string{}
	add := func(l int, r string) {
		if l == Ask && (!opt.AskRules || opt.AskIgnore[r]) {
			return
		}
		if l > level {
			level = l
		}
		for _, x := range reasons {
			if x == r {
				return
			}
		}
		reasons = append(reasons, r)
	}
	if pycompat.Chars(text) > opt.MaxLen {
		add(Ask, "input longer than "+itoa(opt.MaxLen)+" chars was not fully inspected")
		text = pycompat.Prefix(text, opt.MaxLen)
	}
	tl := strings.ToLower(tool)
	body := text
	fileTools := set("read write edit multiedit notebookedit notebookread glob grep ls")
	if fileTools[tl] {
		head := text
		if tl != "grep" {
			if i := strings.Index(head, " ("); i >= 0 {
				head = head[:i]
			}
			if i := strings.Index(head, ": "); i >= 0 {
				head = head[:i]
			}
		}
		for _, r := range pathRules {
			if (r.re.MatchString(head) || r.re.MatchString(dequote(head))) && !(r.reason == ".env file" && !isEnvFile(head)) {
				add(r.level, r.reason)
			}
		}
		body = ""
	} else if tl == "webfetch" || tl == "websearch" {
		for _, r := range urlRules {
			if r.re.MatchString(text) {
				add(r.level, r.reason)
			}
		}
		body = ""
	}
	if body != "" {
		body = stripDataHeredocs(body)
		text2 := strings.ReplaceAll(body, "\\\n", " ")
		view := askView(text2)
		hardened := false
		if tl == "bash" || tl == "shell" || tl == "sh" || tl == "run" || tl == "exec" || tl == "command" || tl == "" {
			hardened = tiers.ContainersAllHardened(text2, cwd)
		}
		for _, pass := range []struct {
			variants []string
			level    int
		}{{[]string{text2, dequote(text2)}, Block}, {[]string{view, dequote(view)}, Ask}} {
			for _, variant := range pass.variants {
				for _, r := range commandRules {
					if r.level == pass.level && r.re.MatchString(variant) && !(hardened && r.reason == "container operation") && !(r.reason == "reads or writes a .env file" && !isEnvFile(variant)) && !(r.reason == "writes to a raw device" && !hasRawDeviceWrite(variant)) && !(r.reason == "changes branch state or the working tree" && applySafeRE.MatchString(variant)) {
						add(r.level, r.reason)
					}
				}
			}
		}
		if tl == "bash" || tl == "shell" || tl == "sh" || tl == "run" || tl == "exec" || tl == "command" || tl == "" {
			for _, x := range specialRules(text2, cwd) {
				add(x[0].(int), x[1].(string))
			}
		}
	}
	return level, reasons
}

type Options struct {
	AskIgnore map[string]bool
	AskRules  bool
	MaxLen    int
}

func DefaultOptions() Options { return Options{AskIgnore: AskIgnore, AskRules: true, MaxLen: 32768} }
func itoa(i int) string       { return strconv.Itoa(i) }
