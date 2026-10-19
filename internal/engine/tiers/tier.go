package tiers

import (
	"strings"
	"unicode/utf8"

	"github.com/MQ37/nod/internal/engine/pycompat"
)

var (
	webURL   = pycompat.Compile(`^https://([A-Za-z0-9.-]+\.[A-Za-z]{2,})(?::443)?(?:/[A-Za-z0-9._~%/+:@=,-]*)?(?:\?[A-Za-z0-9._~%/+:@=,&-]{0,80})?(?:#[\w.-]*)?$`, "")
	webQuery = pycompat.MustCompile(`(?i)(?:token|key|secret|auth|sig|session|password)=`, "")
	webIP    = pycompat.MustCompile(`^[\d.]+$`, "")
)

// Tier classifies a whole shell command line as "T0", "T0b" or "?".
func Tier(cmd, cwd string) string {
	if cmd == "" || pycompat.Chars(cmd) > 20000 {
		return "?"
	}
	units, ok := commandUnits(cmd)
	if !ok || len(units) == 0 {
		return "?"
	}
	best := "T0"
	for _, u := range units {
		t := simple(u, cwd)
		if t == "?" {
			return "?"
		}
		if t == "T0b" {
			best = "T0b"
		}
		if len(u.words) == 2 && u.words[0].text == "cd" {
			if q, ok := norm(u.words[1].text, cwd); ok {
				cwd = q
			}
		}
	}
	return best
}

// FileTool classifies a Read/Edit/Write/WebFetch style tool input.
func FileTool(tool, text, cwd string) string {
	path := pycompat.Strip(strings.Split(strings.Split(strings.Split(text, " (write ")[0], ": replace ")[0], " (multiple")[0])
	if sensitive.Match(path) {
		return "?"
	}
	if tool == "WebFetch" {
		u := pycompat.Strip(text)
		m := webURL.FindStringSubmatch(u)
		if m != nil && !sensitive.Match(u) && !envSecret.Match(u) && !webQuery.Match(u) && !webIP.Match(m[1]) {
			return "T0"
		}
		return "?"
	}
	if tool == "Read" {
		if localPath(path, cwd, true) || (strings.HasPrefix(path, "/") && readableOutside(path)) {
			return "T0"
		}
		return "?"
	}
	if tool == "Edit" || tool == "Write" || tool == "MultiEdit" || tool == "NotebookEdit" {
		if sensitive.Match(text) {
			return "?"
		}
		q, ok := norm(path, cwd)
		if localPath(path, cwd, true) && ok && !execCfg.Match(q) {
			return "T0b"
		}
		return "?"
	}
	return "?"
}

func commandUnits(cmd string) ([]unit, bool) {
	if strings.Contains(cmd, "<<") {
		next, ok := stripCatHeredocs(cmd)
		if !ok {
			return nil, false
		}
		cmd = next
	}
	return lex(cmd)
}

func simple(u unit, cwd string) string {
	for _, a := range u.assigns {
		name, val, _ := strings.Cut(a.text, "=")
		if a.dyn || a.glob || a.tilde || !safeAsg.Match(name) {
			return "?"
		}
		if sensitive.Match(val) {
			return "?"
		}
	}
	rt := redirTier(u.redirs, cwd)
	if rt == "?" {
		return "?"
	}
	ws := u.words
	if len(ws) == 0 {
		return rt
	}
	if ws[0].dyn || ws[0].glob || ws[0].tilde || strings.Contains(ws[0].text, "/") {
		return "?"
	}
	prog := ws[0].text
	args := ws[1:]
	texts := wordTexts(args)
	echoLike := prog == "echo" || prog == "printf"
	for _, a := range args {
		if risky(a) {
			return "?"
		}
		if (a.dyn || a.tilde) && !echoLike {
			return "?"
		}
	}
	if prog == "printf" {
		for _, t := range texts {
			if strings.HasPrefix(t, "-v") {
				return "?"
			}
		}
	}
	tier := rt
	if prog == "jq" {
		for _, t := range texts {
			if !strings.HasPrefix(t, "-") && jqBad.Match(t) {
				return "?"
			}
		}
	}
	if _, ro := roProgs[prog]; ro || echoLike || prog == "cd" {
		if prog == "cd" {
			if len(texts) != 1 || texts[0] == "-" || strings.HasPrefix(texts[0], "-") {
				return "?"
			}
			if localPath(texts[0], cwd, true) {
				return "T0"
			}
			return "?"
		}
		if echoLike {
			return tier
		}
		if !optsOK(texts, roProgs[prog]) {
			return "?"
		}
		if pathCheckProgs[prog] {
			for _, t := range texts {
				if strings.HasPrefix(t, "-") {
					continue
				}
				if strings.HasPrefix(t, "/") || strings.Contains(t, "/") || strings.HasPrefix(t, ".") {
					outside := strings.HasPrefix(t, "/") && !sensitive.Match(t) && outsideReadProgs[prog] && readableOutside(t)
					if !(localPath(t, cwd, true) || outside) {
						return "?"
					}
				}
			}
		}
		return tier
	}
	if prog == "find" {
		return findCmd(texts, tier, cwd)
	}
	if prog == "sed" {
		return sedCmd(texts, tier, cwd)
	}
	if prog == "tee" {
		return teeCmd(texts, cwd)
	}
	return program(prog, texts, args, tier, cwd)
}

func redirTier(rs []redir, cwd string) string {
	t := "T0"
	for _, rd := range rs {
		if rd.w == nil {
			continue
		}
		tw := *rd.w
		if tw.dyn || tw.tilde || tw.glob {
			return "?"
		}
		tgt := tw.text
		if strings.HasSuffix(rd.op, "<") {
			if sensitive.Match(tgt) {
				return "?"
			}
			if _, ok := norm(tgt, cwd); !ok {
				return "?"
			}
			continue
		}
		if tgt == "/dev/null" {
			continue
		}
		q, ok := norm(tgt, cwd)
		if !ok {
			q = ""
		}
		if !localPath(tgt, cwd, true) || execCfg.Match(q) {
			return "?"
		}
		t = "T0b"
	}
	return t
}

func findCmd(texts []string, tier, cwd string) string {
	for _, t := range texts {
		if findBad.Match(t) && t != "-prune" {
			return "?"
		}
	}
	for _, t := range texts {
		if strings.HasPrefix(t, "-") && !findOK.Match(t) {
			return "?"
		}
	}
	var paths []string
	for _, t := range texts {
		if strings.HasPrefix(t, "-") || strings.HasPrefix(t, "(") || strings.HasPrefix(t, ")") || strings.HasPrefix(t, "!") {
			continue
		}
		paths = append(paths, t)
	}
	if len(paths) > 0 {
		t := paths[0]
		outside := strings.HasPrefix(t, "/") && !sensitive.Match(t) && readableOutside(t)
		if t != "." && !localPath(t, cwd, true) && !outside {
			return "?"
		}
	}
	return tier
}

func teeCmd(texts []string, cwd string) string {
	var paths []string
	for _, t := range texts {
		if strings.HasPrefix(t, "-") {
			if t != "-a" && t != "--append" {
				return "?"
			}
			continue
		}
		paths = append(paths, t)
	}
	if len(paths) > 0 && allLocalExec(paths, cwd) {
		return "T0b"
	}
	return "?"
}

func stripCatHeredocs(cmd string) (string, bool) {
	var out strings.Builder
	pos := 0
	for {
		start, end, id, ok := findCatHD(cmd, pos)
		if !ok {
			out.WriteString(cmd[pos:])
			return out.String(), true
		}
		lineStart := strings.LastIndex(cmd[:start], "\n") + 1
		rel := strings.IndexByte(cmd[end:], '\n')
		lineEnd := len(cmd)
		if rel >= 0 {
			lineEnd = end + rel
		}
		owner := cmd[lineStart:start] + " " + cmd[end:lineEnd]
		units, lexOK := lex(owner)
		if !lexOK {
			return "", false
		}
		good := false
		for _, u := range units {
			if len(u.words) == 0 {
				continue
			}
			p := u.words[0].text
			if strings.Contains(p, "/") {
				return "", false
			}
			switch p {
			case "cat", "tee":
				good = true
			case "mkdir", "cd", "echo", "true", "cp", "mv", "touch", "ls", "rm":
			default:
				return "", false
			}
		}
		if !good || lineEnd >= len(cmd) {
			return "", false
		}
		term := lineTerminator(cmd, lineEnd+1, id)
		if term < 0 {
			return "", false
		}
		out.WriteString(cmd[pos:start])
		out.WriteString(cmd[end:lineEnd])
		out.WriteByte('\n')
		if term < len(cmd) {
			pos = term + 1
		} else {
			pos = len(cmd)
		}
	}
}

func findCatHD(s string, from int) (start, end int, id string, ok bool) {
	for from < len(s) {
		rel := strings.Index(s[from:], "<<")
		if rel < 0 {
			return 0, 0, "", false
		}
		start = from + rel
		p := start + 2
		if p < len(s) && s[p] == '-' {
			p++
		}
		for p < len(s) && (s[p] == ' ' || s[p] == '\t') {
			p++
		}
		var quote byte
		if p < len(s) && (s[p] == '\'' || s[p] == '"') {
			quote = s[p]
			p++
		}
		idStart := p
		if p >= len(s) {
			from = start + 1
			continue
		}
		r, w := utf8.DecodeRuneInString(s[p:])
		if !pycompat.IsWord(r) {
			from = start + 1
			continue
		}
		p += w
		for p < len(s) {
			r, w = utf8.DecodeRuneInString(s[p:])
			if !pycompat.IsWord(r) {
				break
			}
			p += w
		}
		id = s[idStart:p]
		if quote != 0 {
			if p >= len(s) || s[p] != quote {
				from = start + 1
				continue
			}
			p++
		}
		return start, p, id, true
	}
	return 0, 0, "", false
}

func lineTerminator(s string, from int, id string) int {
	i := from
	if i > 0 && i < len(s) && (i == 0 || s[i-1] != '\n') {
		nl := strings.IndexByte(s[i:], '\n')
		if nl < 0 {
			return -1
		}
		i += nl + 1
	}
	for i <= len(s) {
		rel := strings.IndexByte(s[i:], '\n')
		line := s[i:]
		if rel >= 0 {
			line = s[i : i+rel]
		}
		if terminatorLine(line, id) {
			return i + len(line)
		}
		if rel < 0 {
			return -1
		}
		i += rel + 1
	}
	return -1
}

func terminatorLine(line, id string) bool {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	if !strings.HasPrefix(line[i:], id) {
		return false
	}
	j := i + len(id)
	for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
		j++
	}
	return j == len(line)
}
