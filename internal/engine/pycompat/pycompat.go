// Package pycompat reproduces the few Python 3 behaviours the engine port depends on: str.isspace/split/strip,
// posixpath.normpath/join, shlex.split(posix=True) and a translator that makes Python regular expressions
// (Unicode \s \w \d, `$` before a final newline) compile on Go's RE2.
package pycompat

import (
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// IsSpace is Python's str.isspace for one rune.
func IsSpace(r rune) bool {
	switch {
	case r >= 0x09 && r <= 0x0d, r >= 0x1c && r <= 0x20, r == 0x85, r == 0xa0, r == 0x1680, r >= 0x2000 && r <= 0x200a,
		r == 0x2028, r == 0x2029, r == 0x202f, r == 0x205f, r == 0x3000:
		return true
	}
	return false
}

// IsWord is Python's re \w (alphanumeric or underscore).
func IsWord(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
}

// IsAlnum is str.isalnum for a single rune.
func IsAlnum(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }

// IsDigit is str.isdigit for a single rune.
func IsDigit(r rune) bool { return unicode.IsDigit(r) || r == '²' || r == '³' || r == '¹' }

// Fields is str.split() without arguments.
func Fields(s string) []string { return strings.FieldsFunc(s, IsSpace) }

// Strip is str.strip() without arguments.
func Strip(s string) string { return strings.TrimFunc(s, IsSpace) }

// Chars is the number of code points in s (Python len).
func Chars(s string) int { return utf8.RuneCountInString(s) }

// Prefix returns the first n code points of s (Python s[:n]).
func Prefix(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	i := 0
	for k := 0; k < n && i < len(s); k++ {
		_, w := utf8.DecodeRuneInString(s[i:])
		i += w
	}
	return s[:i]
}

// Suffix returns the last n code points of s (Python s[-n:]).
func Suffix(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	skip := Chars(s) - n
	i := 0
	for k := 0; k < skip && i < len(s); k++ {
		_, w := utf8.DecodeRuneInString(s[i:])
		i += w
	}
	return s[i:]
}

// Lower is str.lower() (full mapping for the one rune where it differs from Go's simple mapping).
func Lower(s string) string {
	if strings.ContainsRune(s, 'İ') {
		s = strings.ReplaceAll(s, "İ", "i̇")
	}
	return strings.ToLower(s)
}

// ---------------------------------------------------------------- regexp

const (
	spaceBody = `\t-\r\x1c-\x20\x85\xa0\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}`
	wordBody  = `\p{L}\p{N}_`
	eol       = `(?:\n?\z)`
)

// Translate rewrites a Python pattern into RE2 syntax: \s \S \w \W \d \D become their Unicode classes, `$` (no MULTILINE)
// becomes "end, or before a final newline", \Z becomes \z. Lookarounds and backreferences are not translated; compilation fails on them.
func Translate(p string) string {
	var b strings.Builder
	inClass := false
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == '\\' && i+1 < len(p):
			i++
			b.WriteString(escape(p[i], inClass))
		case inClass:
			if c == ']' {
				inClass = false
			}
			b.WriteByte(c)
		case c == '[':
			inClass = true
			b.WriteByte(c)
			if i+1 < len(p) && p[i+1] == '^' {
				i++
				b.WriteByte('^')
			}
			if i+1 < len(p) && p[i+1] == ']' {
				i++
				b.WriteString(`\]`)
			}
		case c == '$':
			b.WriteString(eol)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func escape(c byte, inClass bool) string {
	switch c {
	case 's':
		return wrap(spaceBody, false, inClass)
	case 'w':
		return wrap(wordBody, false, inClass)
	case 'd':
		return wrap(`\p{Nd}`, false, inClass)
	case 'S':
		return wrap(spaceBody, true, inClass)
	case 'W':
		return wrap(wordBody, true, inClass)
	case 'D':
		return wrap(`\p{Nd}`, true, inClass)
	case 'Z':
		return `\z`
	}
	return `\` + string(c)
}

func wrap(body string, negate, inClass bool) string {
	if inClass {
		if negate {
			panic("pycompat: negated shorthand inside a class is not supported")
		}
		return body
	}
	if negate {
		return "[^" + body + "]"
	}
	return "[" + body + "]"
}

// Compile compiles a Python pattern; flags may contain "i" (IGNORECASE) and "s" (DOTALL).
func Compile(p, flags string) *regexp.Regexp {
	prefix := ""
	if flags != "" {
		prefix = "(?" + flags + ")"
	}
	return regexp.MustCompile(prefix + Translate(p))
}

// CompileFull is Compile for a pattern used with re.fullmatch.
func CompileFull(p, flags string) *regexp.Regexp {
	return Compile("^(?:"+p+")$", flags)
}

// ---------------------------------------------------------------- posixpath

// Normpath is posixpath.normpath.
func Normpath(path string) string {
	if path == "" {
		return "."
	}
	initial := 0
	if strings.HasPrefix(path, "/") {
		initial = 1
		if strings.HasPrefix(path, "//") && !strings.HasPrefix(path, "///") {
			initial = 2
		}
	}
	var out []string
	for _, comp := range strings.Split(path, "/") {
		if comp == "" || comp == "." {
			continue
		}
		if comp != ".." || (initial == 0 && len(out) == 0) || (len(out) > 0 && out[len(out)-1] == "..") {
			out = append(out, comp)
		} else if len(out) > 0 {
			out = out[:len(out)-1]
		}
	}
	res := strings.Repeat("/", initial) + strings.Join(out, "/")
	if res == "" {
		return "."
	}
	return res
}

// Join is posixpath.join for two parts.
func Join(a, b string) string {
	switch {
	case strings.HasPrefix(b, "/"):
		return b
	case a == "" || strings.HasSuffix(a, "/"):
		return a + b
	}
	return a + "/" + b
}

// Basename is posixpath.basename.
func Basename(p string) string { return p[strings.LastIndex(p, "/")+1:] }

// ---------------------------------------------------------------- shlex

// ErrShlex is the Python ValueError of shlex.split (unterminated quote or trailing escape).
var ErrShlex = errors.New("shlex: no closing quotation or escaped character")

func isShlexSpace(r rune) bool { return r == ' ' || r == '\t' || r == '\r' || r == '\n' }

// ShlexSplit is shlex.split(s, posix=True) (comments off, whitespace_split on).
func ShlexSplit(s string) ([]string, error) {
	var out []string
	rs := []rune(s)
	i := 0
	for {
		tok, ok, err := shlexToken(rs, &i)
		if err != nil {
			return nil, err
		}
		if !ok {
			return out, nil
		}
		out = append(out, tok)
	}
}

// shlexToken reads one token; ok is false at end of input.
func shlexToken(rs []rune, pos *int) (string, bool, error) {
	var tok []rune
	quoted := false
	state := ' ' // ' ' between tokens, 'a' in a word, '\'' or '"' inside quotes, '\\' after an escape
	var escapedState rune
	for {
		if *pos >= len(rs) {
			switch state {
			case '\'', '"':
				return "", false, ErrShlex
			case '\\':
				return "", false, ErrShlex
			}
			return string(tok), len(tok) > 0 || quoted, nil
		}
		c := rs[*pos]
		*pos++
		switch {
		case state == ' ' || state == 'a':
			switch {
			case isShlexSpace(c):
				if len(tok) > 0 || quoted {
					return string(tok), true, nil
				}
				state = ' '
			case c == '\\':
				escapedState, state = 'a', '\\'
			case c == '\'' || c == '"':
				state = c
			default:
				tok = append(tok, c)
				state = 'a'
			}
		case state == '\'' || state == '"':
			quoted = true
			switch {
			case c == state:
				state = 'a'
			case c == '\\' && state == '"':
				escapedState, state = state, '\\'
			default:
				tok = append(tok, c)
			}
		default: // after a backslash
			if (escapedState == '\'' || escapedState == '"') && c != '\\' && c != escapedState {
				tok = append(tok, '\\')
			}
			tok = append(tok, c)
			state = escapedState
		}
	}
}
