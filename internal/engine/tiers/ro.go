package tiers

import (
	"strings"
	"unicode"
)

// optSpec is a closed option grammar: long names match exactly, so abbreviations are rejected.
type optSpec struct {
	short, vshort string
	long, vlong   []string
	max           int
	hasMax        bool
}

func optsOK(args []string, sp optSpec) bool {
	pos := 0
	for i := 0; i < len(args); {
		a := args[i]
		if a == "--" {
			pos += len(args) - i - 1
			break
		}
		if strings.HasPrefix(a, "--") {
			name, _, hasEq := strings.Cut(a, "=")
			if strSliceHas(sp.long, name) {
				i++
				continue
			}
			if strSliceHas(sp.vlong, name) {
				if hasEq {
					i++
				} else {
					i += 2
				}
				continue
			}
			return false
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			rs := []rune(a)
			j := 1
			for j < len(rs) {
				ch := rs[j]
				if strings.ContainsRune(sp.vshort, ch) {
					if j+1 < len(rs) {
						break
					}
					i++
					break
				}
				if !strings.ContainsRune(sp.short, ch) && !unicode.IsDigit(ch) {
					return false
				}
				j++
			}
			i++
			continue
		}
		pos++
		i++
	}
	return !sp.hasMax || pos <= sp.max
}

func strSliceHas(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func opt(short string, long []string, vshort string, vlong []string, max int, has bool) optSpec {
	return optSpec{short, vshort, long, vlong, max, has}
}

var roProgs = map[string]optSpec{
	"ls":        opt("lahAFRSXrtuc1dLpgGoiknsqQUvwxC", []string{"--all", "--almost-all", "--human-readable", "--recursive", "--color", "--classify", "--directory", "--size", "--reverse", "--time", "--sort", "--format", "--group-directories-first"}, "wITw", []string{"--hide", "--ignore", "--time-style", "--width", "--tabsize"}, 0, false),
	"cat":       opt("nbsvAeEtTu", []string{"--number", "--number-nonblank", "--squeeze-blank", "--show-all", "--show-ends", "--show-tabs", "--show-nonprinting"}, "", nil, 0, false),
	"head":      opt("cnqvz", []string{"--bytes", "--lines", "--quiet", "--silent", "--verbose"}, "cn", []string{"--bytes", "--lines"}, 0, false),
	"tail":      opt("cnqvsz", []string{"--bytes", "--lines", "--quiet", "--silent", "--verbose", "--follow", "--retry"}, "cns", []string{"--bytes", "--lines", "--sleep-interval"}, 0, false),
	"wc":        opt("clmwL", []string{"--bytes", "--chars", "--lines", "--words", "--max-line-length"}, "", nil, 0, false),
	"grep":      opt("EFGPiyvcLlomnqsHhbTzaIrRwxUAB0123456789C", []string{"--extended-regexp", "--fixed-strings", "--ignore-case", "--invert-match", "--count", "--files-with-matches", "--files-without-match", "--only-matching", "--line-number", "--recursive", "--dereference-recursive", "--word-regexp", "--line-regexp", "--no-filename", "--with-filename", "--quiet", "--silent", "--no-messages", "--color", "--null", "--text"}, "AmBCe", []string{"--regexp", "--max-count", "--after-context", "--before-context", "--context", "--include", "--exclude", "--exclude-dir", "--include-dir", "--binary-files"}, 0, false),
	"sort":      opt("bdfgiMhnRrsuVz", []string{"--reverse", "--numeric-sort", "--unique", "--ignore-case", "--human-numeric-sort", "--version-sort", "--stable", "--ignore-leading-blanks", "--dictionary-order", "--general-numeric-sort", "--month-sort", "--random-sort"}, "kt", []string{"--key", "--field-separator"}, 0, false),
	"uniq":      opt("cdiuz", []string{"--count", "--repeated", "--ignore-case", "--unique"}, "fsw", []string{"--skip-fields", "--skip-chars", "--check-chars"}, 1, true),
	"cut":       opt("bcfdsnz", []string{"--bytes", "--characters", "--fields", "--delimiter", "--only-delimited"}, "bcfd", []string{"--bytes", "--characters", "--fields", "--delimiter"}, 0, false),
	"tr":        opt("cdsC", []string{"--complement", "--delete", "--squeeze-repeats"}, "", nil, 2, true),
	"diff":      opt("uqrNwbBiyEZaTcsd", []string{"--unified", "--brief", "--recursive", "--new-file", "--ignore-all-space", "--ignore-space-change", "--ignore-case", "--side-by-side", "--color", "--ignore-blank-lines", "--context"}, "UCW", []string{"--unified", "--context", "--width"}, 0, false),
	"cmp":       opt("lsbi", []string{"--silent", "--quiet", "--verbose"}, "in", []string{"--ignore-initial", "--bytes"}, 2, true),
	"stat":      opt("cLfts", []string{"--format", "--dereference", "--file-system", "--terse"}, "c", []string{"--format"}, 0, false),
	"file":      opt("bLiNsz", []string{"--brief", "--mime", "--mime-type", "--mime-encoding", "--dereference"}, "", nil, 0, false),
	"du":        opt("achkmsLxd", []string{"--all", "--human-readable", "--summarize", "--max-depth", "--total", "--apparent-size", "--block-size"}, "dBt", []string{"--max-depth", "--block-size", "--threshold"}, 0, false),
	"df":        opt("hHTiakPl", []string{"--human-readable", "--total", "--local", "--type", "--print-type"}, "t", []string{"--type", "--exclude-type"}, 0, false),
	"tree":      opt("aCdfFghilnpqrstuvxADL", []string{"--charset", "--dirsfirst", "--noreport"}, "LIPH", nil, 0, false),
	"pwd":       opt("LP", nil, "", nil, 0, true),
	"whoami":    opt("", nil, "", nil, 0, true),
	"id":        opt("gGnruZ", nil, "", nil, 1, true),
	"uname":     opt("asnrvmpio", []string{"--all", "--kernel-name", "--kernel-release", "--machine"}, "", nil, 0, true),
	"true":      opt("", nil, "", nil, 0, false),
	"false":     opt("", nil, "", nil, 0, false),
	"which":     opt("a", nil, "", nil, 0, false),
	"type":      opt("aftpP", nil, "", nil, 0, false),
	"basename":  opt("as", []string{"--suffix", "--multiple"}, "s", []string{"--suffix"}, 0, false),
	"dirname":   opt("z", nil, "", nil, 0, false),
	"realpath":  opt("eLmPsqz", []string{"--canonicalize-existing", "--canonicalize-missing", "--no-symlinks", "--relative-to", "--relative-base"}, "", []string{"--relative-to", "--relative-base"}, 0, false),
	"readlink":  opt("efmnqsvz", nil, "", nil, 0, false),
	"column":    opt("tnsxcwpeo", []string{"--table", "--separator"}, "ts", []string{"--separator", "--output-separator"}, 0, false),
	"nl":        opt("bpvwnsi", nil, "bpvwns", nil, 0, false),
	"fold":      opt("bsw", []string{"--width", "--spaces"}, "w", []string{"--width"}, 0, false),
	"md5sum":    opt("bct", nil, "", nil, 0, false),
	"sha256sum": opt("bct", nil, "", nil, 0, false),
	"sha1sum":   opt("bct", nil, "", nil, 0, false),
	"od":        opt("AjNtvxcdoaCfs", nil, "AjNtw", nil, 0, false),
	"strings":   opt("afont", nil, "no", nil, 0, false),
	"ps":        opt("aAxeofuUCpPqtTlwjHVLmMnrs", []string{"--forest", "--sort", "--no-headers", "--format"}, "oOpqtUCuGg", []string{"--sort", "--format"}, 0, false),
	"pgrep":     opt("flanovxiPgtuU", nil, "PgtuU", nil, 0, false),
	"lsof":      opt("iPnlRtUV", nil, "iaudpcsTF", nil, 0, false),
	"ss":        opt("tulnpoeamsrHOA46", nil, "A", nil, 0, false),
	"free":      opt("bkmghlstw", []string{"--human", "--mega", "--giga"}, "s", nil, 0, false),
	"uptime":    opt("ps", nil, "", nil, 0, false),
	"nproc":     opt("", []string{"--all"}, "", nil, 0, false),
	"paste":     opt("sd", []string{"--serial", "--delimiters"}, "d", []string{"--delimiters"}, 0, false),
	"comm":      opt("123", nil, "", nil, 0, false),
	"join":      opt("aveiot12", nil, "aveiot12", nil, 0, false),
	"rev":       opt("", nil, "", nil, 0, false),
	"tac":       opt("", nil, "", nil, 0, false),
	"seq":       opt("fsw", nil, "fs", nil, 0, false),
	"sleep":     opt("", nil, "", nil, 1, true),
	"lsblk":     opt("abdfimnoprstSDlP", []string{"--all", "--bytes", "--json", "--list", "--output", "--paths"}, "o", []string{"--output"}, 0, false),
	"findmnt":   opt("lnrsDkvfAUuRm", []string{"--list", "--noheadings", "--raw", "--json"}, "oTt", nil, 0, false),
	"jq":        opt("rcesSMnaCj", []string{"--raw-output", "--compact-output", "--exit-status", "--sort-keys", "--monochrome-output", "--null-input", "--slurp", "--tab"}, "", []string{"--arg", "--argjson", "--indent"}, 0, false),
}

var pathCheckProgs = map[string]bool{
	"cat": true, "head": true, "tail": true, "wc": true, "grep": true, "ls": true, "stat": true, "file": true,
	"du": true, "tree": true, "diff": true, "cmp": true, "sort": true, "uniq": true, "cut": true, "od": true,
	"strings": true, "md5sum": true, "sha256sum": true, "sha1sum": true, "nl": true, "fold": true, "column": true,
	"paste": true, "comm": true, "join": true, "rev": true, "tac": true, "realpath": true, "readlink": true,
}

var outsideReadProgs = map[string]bool{
	"ls": true, "stat": true, "file": true, "du": true, "df": true, "readlink": true, "realpath": true,
	"cat": true, "head": true, "tail": true, "wc": true, "diff": true,
}
