package hosts

import (
	"fmt"
	"strings"
)

// UnifiedDiff renders a unified diff (3 lines of context) between a and b; "" when equal.
func UnifiedDiff(path, a, b string) string {
	if a == b {
		return ""
	}
	al, bl := splitLines(a), splitLines(b)
	type op struct {
		kind byte // ' ', '-', '+'
		text string
	}
	var ops []op
	// trim common prefix/suffix, LCS on the middle (config files are small)
	p := 0
	for p < len(al) && p < len(bl) && al[p] == bl[p] {
		p++
	}
	s := 0
	for s < len(al)-p && s < len(bl)-p && al[len(al)-1-s] == bl[len(bl)-1-s] {
		s++
	}
	for _, l := range al[:p] {
		ops = append(ops, op{' ', l})
	}
	am, bm := al[p:len(al)-s], bl[p:len(bl)-s]
	if len(am)*len(bm) > 4_000_000 {
		for _, l := range am {
			ops = append(ops, op{'-', l})
		}
		for _, l := range bm {
			ops = append(ops, op{'+', l})
		}
	} else {
		n, m := len(am), len(bm)
		dp := make([][]int, n+1)
		for i := range dp {
			dp[i] = make([]int, m+1)
		}
		for i := n - 1; i >= 0; i-- {
			for j := m - 1; j >= 0; j-- {
				if am[i] == bm[j] {
					dp[i][j] = dp[i+1][j+1] + 1
				} else if dp[i+1][j] >= dp[i][j+1] {
					dp[i][j] = dp[i+1][j]
				} else {
					dp[i][j] = dp[i][j+1]
				}
			}
		}
		i, j := 0, 0
		for i < n || j < m {
			switch {
			case i < n && j < m && am[i] == bm[j]:
				ops = append(ops, op{' ', am[i]})
				i++
				j++
			case i < n && (j == m || dp[i+1][j] >= dp[i][j+1]):
				ops = append(ops, op{'-', am[i]})
				i++
			default:
				ops = append(ops, op{'+', bm[j]})
				j++
			}
		}
	}
	for _, l := range al[len(al)-s:] {
		ops = append(ops, op{' ', l})
	}
	// Context lines of a secrets file (.env and friends) could print neighbouring secrets into a
	// plan, a terminal or an agent transcript: show only the changed lines for those.
	ctx := 3
	if secretFile(path) {
		ctx = 0
	}
	var out strings.Builder
	fmt.Fprintf(&out, "--- %s\n+++ %s\n", orDev(a, path), orDev(b, path))
	type rng struct{ first, last int }
	var hunks []rng
	for k, o := range ops {
		if o.kind == ' ' {
			continue
		}
		if n := len(hunks); n > 0 && k-hunks[n-1].last-1 <= 2*ctx {
			hunks[n-1].last = k
		} else {
			hunks = append(hunks, rng{k, k})
		}
	}
	for _, h := range hunks {
		start, end := max(0, h.first-ctx), min(len(ops), h.last+1+ctx)
		aStart, bStart, aN, bN := 1, 1, 0, 0
		for k := 0; k < start; k++ {
			if ops[k].kind != '+' {
				aStart++
			}
			if ops[k].kind != '-' {
				bStart++
			}
		}
		var body strings.Builder
		for k := start; k < end; k++ {
			if ops[k].kind != '+' {
				aN++
			}
			if ops[k].kind != '-' {
				bN++
			}
			body.WriteByte(ops[k].kind)
			body.WriteString(ops[k].text)
			body.WriteByte('\n')
		}
		if aN == 0 {
			aStart--
		}
		if bN == 0 {
			bStart--
		}
		fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@\n%s", aStart, aN, bStart, bN, body.String())
	}
	return out.String()
}

func secretFile(path string) bool {
	base := strings.ToLower(path[strings.LastIndexAny(path, "/\\")+1:])
	return strings.HasPrefix(base, ".env") || strings.HasSuffix(base, ".env") || strings.Contains(base, "secret") || strings.Contains(base, "credential") || strings.Contains(base, "token")
}

func orDev(content, path string) string {
	if content == "" {
		return "/dev/null"
	}
	return path
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}
