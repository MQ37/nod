package pycompat

import (
	"regexp"
	"strings"
)

// Heredoc is one `<<ID ... ID` block found in a command text.
type Heredoc struct {
	Start     int // index of `<<`
	HeaderEnd int // index just after the newline that ends the operator line
	BodyEnd   int // end of the body (the newline before the terminator, or the end of the text)
	End       int // end of the whole block (terminator included)
	ID        string
}

var heredocHeader = Compile(`<<-?\s*(?:'(\w+)'|"(\w+)"|(\w+))[^\n]*\n`, "")

// FindHeredocs scans left to right for heredocs the way the Python reference does with
//
//	<<-?\s*(['"]?)(\w+)\1[^\n]*\n(.*?)(?:\n[ \t]*\2[ \t]*(?=\n|$)|\Z)     (unterminated = true)
//	<<-?\s*(['"]?)(\w+)\1[^\n]*\n(.*?)\n[ \t]*\2[ \t]*(?=\n|$)           (unterminated = false)
//
// RE2 has no backreferences or lookahead, and for an unquoted id the Python engine backtracks to shorter ids, so this is code.
func FindHeredocs(s string, unterminated bool) []Heredoc {
	var out []Heredoc
	pos := 0
	for pos <= len(s) {
		m := heredocHeader.FindStringSubmatchIndex(s[pos:])
		if m == nil {
			break
		}
		start, hend := pos+m[0], pos+m[1]
		h, ok := matchHeredoc(s, start, hend, m, pos, unterminated)
		if !ok {
			pos = start + 1
			continue
		}
		out = append(out, h)
		pos = h.End
	}
	return out
}

func matchHeredoc(s string, start, hend int, m []int, off int, unterminated bool) (Heredoc, bool) {
	var ids []string
	quoted := false
	for g := 1; g <= 3; g++ {
		if m[2*g] >= 0 {
			id := s[off+m[2*g] : off+m[2*g+1]]
			quoted = g != 3
			ids = []string{id}
			if !quoted && !unterminated {
				for rs := []rune(id); len(rs) > 1; {
					rs = rs[:len(rs)-1]
					ids = append(ids, string(rs))
				}
			}
			break
		}
	}
	_ = quoted
	for _, id := range ids {
		if bodyEnd, end, ok := findTerminator(s, hend, id); ok {
			return Heredoc{start, hend, bodyEnd, end, id}, true
		}
		if unterminated {
			return Heredoc{start, hend, len(s), len(s), id}, true
		}
	}
	return Heredoc{}, false
}

// findTerminator finds the earliest `\n[ \t]*ID[ \t]*` (followed by a newline or the end) at or after from.
func findTerminator(s string, from int, id string) (bodyEnd, end int, ok bool) {
	for p := from; p < len(s); p++ {
		if s[p] != '\n' {
			continue
		}
		q := skipBlanks(s, p+1)
		if !strings.HasPrefix(s[q:], id) {
			continue
		}
		r := skipBlanks(s, q+len(id))
		if r == len(s) || s[r] == '\n' {
			return p, r, true
		}
	}
	return 0, 0, false
}

func skipBlanks(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

// LineTerminator returns the end index of the first line `^[ \t]*ID[ \t]*$` (multiline) at or after from, or -1.
func LineTerminator(s string, from int, id string) int {
	re := regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(id) + `[ \t]*$`)
	loc := re.FindStringIndex(s[from:])
	if loc == nil {
		return -1
	}
	return from + loc[1]
}
