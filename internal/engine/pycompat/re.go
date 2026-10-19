package pycompat

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Matcher is a Python re pattern compiled for RE2. Word boundaries (\b) are Unicode
// for ASCII text (identical to Python) and, when the text contains non-ASCII word
// characters, those characters are folded to '_' so RE2's ASCII \b sees them as word
// characters. Folding can false-positive a pattern that both uses \b and an ASCII
// class that includes '_', such as [A-Za-z0-9_-]; Match reports that separately.
type Matcher struct {
	re       *regexp.Regexp
	boundary bool
}

// MustCompile compiles a Python pattern. flags may contain i and s.
func MustCompile(pattern, flags string) Matcher {
	return Matcher{re: Compile(pattern, flags), boundary: hasWordBoundary(pattern)}
}

func hasWordBoundary(pattern string) bool {
	inClass := false
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c == '\\' && i+1 < len(pattern) {
			if !inClass && pattern[i+1] == 'b' {
				return true
			}
			i++
			continue
		}
		if inClass {
			if c == ']' {
				inClass = false
			}
			continue
		}
		if c == '[' {
			inClass = true
		}
	}
	return false
}

// Match is Python re.search (unanchored).
func (m Matcher) Match(s string) bool {
	if m.boundary && HasUnicodeWord(s) {
		s = FoldWords(s)
	}
	return m.re.MatchString(s)
}

func (m Matcher) MatchString(s string) bool { return m.Match(s) }

// FindStringIndex is the byte span of the first match. The caller must not fold.
func (m Matcher) FindStringIndex(s string) []int { return m.re.FindStringIndex(s) }

func (m Matcher) FindAllString(s string, n int) []string { return m.re.FindAllString(s, n) }

func (m Matcher) FindAllStringSubmatch(s string, n int) [][]string {
	return m.re.FindAllStringSubmatch(s, n)
}

func (m Matcher) ReplaceAllString(s, replacement string) string {
	return m.re.ReplaceAllString(s, replacement)
}

// FindAllStringSubmatchIndex returns non-overlapping submatch spans.
func (m Matcher) FindAllStringSubmatchIndex(s string, n int) [][]int {
	return m.re.FindAllStringSubmatchIndex(s, n)
}

// HasUnicodeWord reports whether s contains a non-ASCII Python word character.
func HasUnicodeWord(s string) bool {
	for _, r := range s {
		if r >= 128 && IsWord(r) {
			return true
		}
	}
	return false
}

// FoldWords maps non-ASCII word characters to '_' so RE2's ASCII \b matches Python's.
func FoldWords(s string) string {
	if !HasUnicodeWord(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r >= 128 && IsWord(r) {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// WordAt reports a Python word boundary at byte offset p.
func WordAt(s string, p int) bool {
	left, right := false, false
	if p > 0 {
		r, _ := utf8.DecodeLastRuneInString(s[:p])
		left = IsWord(r)
	}
	if p < len(s) {
		r, _ := utf8.DecodeRuneInString(s[p:])
		right = IsWord(r)
	}
	return left != right
}
