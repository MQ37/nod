package tiers

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/MQ37/nod/internal/engine/pycompat"
)

// word is one shell word. Flags record constructs the rest of the tier must not trust.
type word struct {
	text          string
	dyn, glob     bool
	tilde, quoted bool
}

type redir struct {
	op string
	w  *word // nil: file-descriptor duplication, no path
}

type unit struct {
	assigns []word
	words   []word
	redirs  []redir
	ctl     string
}

var keywords = map[string]bool{
	"if": true, "then": true, "else": true, "elif": true, "fi": true,
	"for": true, "while": true, "until": true, "do": true, "done": true,
	"case": true, "esac": true, "function": true, "select": true, "time": true,
	"coproc": true, "!": true, "[[": true, "]]": true, "{": true, "}": true,
}

// lex tokenises one command line. ok is false when the line uses anything the lexer does not model.
func lex(cmd string) ([]unit, bool) {
	cmd = strings.ReplaceAll(cmd, "\\\n", "")
	rs := []rune(cmd)
	n := len(rs)
	var units []unit
	cur := unit{}
	i := 0
	var pending *string
	var w *word

	endWord := func() {
		if w == nil {
			return
		}
		if pending != nil {
			ww := *w
			cur.redirs = append(cur.redirs, redir{*pending, &ww})
			pending = nil
		} else if len(cur.words) == 0 && assignPrefix(w.text) {
			cur.assigns = append(cur.assigns, *w)
		} else {
			cur.words = append(cur.words, *w)
		}
		w = nil
	}
	endUnit := func(ctlNext string) bool {
		endWord()
		if pending != nil {
			return false
		}
		if len(cur.words) > 0 || len(cur.assigns) > 0 || len(cur.redirs) > 0 {
			units = append(units, cur)
		} else if ctlNext != "" {
			return false
		}
		cur = unit{ctl: ctlNext}
		return true
	}

	for i < n {
		c := rs[i]
		if c == ' ' || c == '\t' {
			endWord()
			i++
			continue
		}
		if c == '#' && w == nil {
			if i > 0 && !strings.ContainsRune(" \t\n;&|", rs[i-1]) {
				return nil, false
			}
			j := indexRune(rs, '\n', i)
			if j < 0 {
				i = n
			} else {
				i = j
			}
			continue
		}
		if c == '\n' || c == ';' {
			if i+1 < n && rs[i] == ';' && rs[i+1] == ';' {
				return nil, false
			}
			empty := w == nil && len(cur.words) == 0 && len(cur.assigns) == 0 && len(cur.redirs) == 0
			if c == '\n' && empty && pending == nil {
				i++
				continue
			}
			if c == ';' && empty {
				return nil, false
			}
			if !endUnit(";") {
				return nil, false
			}
			i++
			continue
		}
		if c == '&' {
			if hasPrefix(rs, i, "&&") {
				if !endUnit("&&") {
					return nil, false
				}
				i += 2
				continue
			}
			if hasPrefix(rs, i, "&>>") || hasPrefix(rs, i, "&>") {
				endWord()
				k := 2
				if hasPrefix(rs, i, "&>>") {
					k = 3
				}
				op := string(rs[i : i+k])
				pending = &op
				i += k
				continue
			}
			return nil, false
		}
		if c == '|' {
			if hasPrefix(rs, i, "||") {
				if !endUnit("||") {
					return nil, false
				}
				i += 2
				continue
			}
			if hasPrefix(rs, i, "|&") {
				return nil, false
			}
			if !endUnit("|") {
				return nil, false
			}
			i++
			continue
		}
		if c == '(' || c == ')' {
			return nil, false
		}
		if c == '<' || c == '>' {
			if hasPrefix(rs, i, "<(") || hasPrefix(rs, i, ">(") {
				return nil, false
			}
			if hasPrefix(rs, i, "<<<") || hasPrefix(rs, i, "<<") {
				return nil, false
			}
			fd := ""
			if w != nil && !w.quoted && allDigits(w.text) {
				fd = w.text
				w = nil
			} else {
				endWord()
			}
			op, olen := redirOp(rs[i:])
			i += olen
			if op == ">&" || op == "<&" {
				if _, mend, ok := fdDup(rs[i:]); ok {
					i += mend
					cur.redirs = append(cur.redirs, redir{fd + op, nil})
					continue
				}
			}
			p := fd + op
			pending = &p
			continue
		}
		if w == nil {
			w = &word{}
		}
		if c == '\\' {
			if i+1 >= n {
				return nil, false
			}
			w.text += string(rs[i+1])
			w.quoted = true
			i += 2
			continue
		}
		if c == '\'' {
			j := indexRune(rs, '\'', i+1)
			if j < 0 {
				return nil, false
			}
			w.text += string(rs[i+1 : j])
			w.quoted = true
			i = j + 1
			continue
		}
		if c == '"' {
			j := i + 1
			buf := ""
			for j < n && rs[j] != '"' {
				if rs[j] == '\\' && j+1 < n {
					buf += string(rs[j+1])
					j += 2
					continue
				}
				if rs[j] == '`' || (rs[j] == '$' && j+1 < n && rs[j+1] == '(') {
					return nil, false
				}
				if rs[j] == '$' {
					w.dyn = true
				}
				buf += string(rs[j])
				j++
			}
			if j >= n {
				return nil, false
			}
			w.text += buf
			w.quoted = true
			i = j + 1
			continue
		}
		if c == '`' {
			return nil, false
		}
		if c == '$' {
			nx := rune(0)
			if i+1 < n {
				nx = rs[i+1]
			}
			if nx == '(' || nx == '\'' || nx == '"' || nx == '[' {
				return nil, false
			}
			if nx == '{' {
				if m, mend, ok := matchBraceVar(rs[i:]); ok {
					w.text += m
					w.dyn = true
					i += mend
					continue
				}
				return nil, false
			}
			if m, name, mend, ok := matchDollar(rs[i:]); ok {
				w.text += m
				if name != "PWD" {
					w.dyn = true
				}
				i += mend
				continue
			}
			w.text += string(c)
			i++
			continue
		}
		if c == '{' {
			if m, mend, ok := matchBrace(rs[i:]); ok {
				w.dyn = true
				w.text += m
				i += mend
				continue
			}
			if w.text == "" && (i+1 >= n || rs[i+1] == ' ' || rs[i+1] == '\t' || rs[i+1] == '\n') {
				return nil, false
			}
			w.text += string(c)
			i++
			continue
		}
		if c == '}' {
			if w.text == "" && (i+1 >= n || strings.ContainsRune(" \t\n;&|", rs[i+1])) {
				return nil, false
			}
			w.text += string(c)
			i++
			continue
		}
		if c == '~' && w.text == "" {
			w.tilde = true
		}
		if c == '*' || c == '?' || c == '[' {
			w.glob = true
		}
		if c == '!' && w.text == "" && len(cur.words) == 0 && len(cur.assigns) == 0 {
			return nil, false
		}
		w.text += string(c)
		i++
	}
	if !endUnit("") {
		return nil, false
	}
	for _, u := range units {
		if len(u.words) > 0 && keywords[u.words[0].text] && !u.words[0].quoted {
			return nil, false
		}
	}
	return units, true
}

func hasPrefix(rs []rune, i int, s string) bool {
	for _, r := range s {
		if i >= len(rs) || rs[i] != r {
			return false
		}
		i++
	}
	return true
}

func indexRune(rs []rune, r rune, from int) int {
	for i := from; i < len(rs); i++ {
		if rs[i] == r {
			return i
		}
	}
	return -1
}

func assignPrefix(s string) bool {
	r, w := utf8.DecodeRuneInString(s)
	if !isNameStart(r) {
		return false
	}
	for i := w; i < len(s); {
		r, w = utf8.DecodeRuneInString(s[i:])
		if r == '=' {
			return true
		}
		if !isNameCont(r) {
			return false
		}
		i += w
	}
	return false
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !isDecimal(r) {
			return false
		}
	}
	return true
}

func isDecimal(r rune) bool { return unicode.Is(unicode.Nd, r) }

// redirOp matches the longest operator at the start of rs.
func redirOp(rs []rune) (string, int) {
	for _, op := range []string{">>", ">&", ">|", "<&", "<>", ">", "<"} {
		if hasPrefix(rs, 0, op) {
			return op, len([]rune(op))
		}
	}
	return string(rs[0]), 1
}

// fdDup matches \s*(\d+|-) not followed by a word char or ./~$ -
func fdDup(rs []rune) (string, int, bool) {
	i := 0
	for i < len(rs) && pycompat.IsSpace(rs[i]) {
		i++
	}
	if i >= len(rs) {
		return "", 0, false
	}
	start := i
	if rs[i] == '-' {
		i++
	} else if isDecimal(rs[i]) {
		for i < len(rs) && isDecimal(rs[i]) {
			i++
		}
	} else {
		return "", 0, false
	}
	if i < len(rs) {
		n := rs[i]
		if pycompat.IsWord(n) || n == '.' || n == '/' || n == '~' || n == '$' || n == '-' {
			return "", 0, false
		}
	}
	return string(rs[start:i]), i, true
}

func matchBraceVar(rs []rune) (string, int, bool) {
	// ${NAME}
	if len(rs) < 4 || rs[0] != '$' || rs[1] != '{' {
		return "", 0, false
	}
	i := 2
	if i >= len(rs) || !isNameStart(rs[i]) {
		return "", 0, false
	}
	i++
	for i < len(rs) && isNameCont(rs[i]) {
		i++
	}
	if i >= len(rs) || rs[i] != '}' {
		return "", 0, false
	}
	return string(rs[:i+1]), i + 1, true
}

func matchDollar(rs []rune) (string, string, int, bool) {
	// $NAME or one of 0-9 ? @ * # $ ! -
	if len(rs) < 2 || rs[0] != '$' {
		return "", "", 0, false
	}
	c := rs[1]
	if strings.ContainsRune("0123456789?@*#$!-", c) {
		return string(rs[:2]), string(c), 2, true
	}
	if !isNameStart(c) {
		return "", "", 0, false
	}
	i := 2
	for i < len(rs) && isNameCont(rs[i]) {
		i++
	}
	return string(rs[:i]), string(rs[1:i]), i, true
}

func matchBrace(rs []rune) (string, int, bool) {
	// {[^{}\s]*(,|..)[^{}\s]*}
	if len(rs) == 0 || rs[0] != '{' {
		return "", 0, false
	}
	i := 1
	for i < len(rs) && rs[i] != '{' && rs[i] != '}' && !pycompat.IsSpace(rs[i]) {
		i++
	}
	if i+1 >= len(rs) {
		return "", 0, false
	}
	sep := 0
	if rs[i] == ',' {
		sep = 1
	} else if rs[i] == '.' && i+1 < len(rs) && rs[i+1] == '.' {
		sep = 2
	} else {
		return "", 0, false
	}
	j := i + sep
	for j < len(rs) && rs[j] != '{' && rs[j] != '}' && !pycompat.IsSpace(rs[j]) {
		j++
	}
	if j >= len(rs) || rs[j] != '}' {
		return "", 0, false
	}
	return string(rs[:j+1]), j + 1, true
}

func isNameStart(r rune) bool {
	return r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z'
}

func isNameCont(r rune) bool {
	return isNameStart(r) || r >= '0' && r <= '9'
}
