// Package cli is the nod command dispatcher: global flags, flag parsing,
// help, shell completion and the exit-code contract. Stdlib only.
package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// ProcessEnv adapts the current process streams and environment for the CLI.
// TTY detection is deliberately injectable in tests; the binary sets conservative
// values because the standard library has no portable TTY probe.
func ProcessEnv() Env {
	stdin, _ := os.Stdin.Stat()
	stdout, _ := os.Stdout.Stat()
	return Env{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv,
		StdinTTY:  stdin != nil && stdin.Mode()&os.ModeCharDevice != 0,
		StdoutTTY: stdout != nil && stdout.Mode()&os.ModeCharDevice != 0}
}

// Kind is the value type of a flag.
type Kind int

// Flag kinds.
const (
	Bool Kind = iota
	String
	Int
)

// Flag describes one command-line flag.
type Flag struct {
	Name    string // long name without dashes
	Short   string // optional single letter
	Kind    Kind
	Default string
	Arg     string // placeholder shown in help, e.g. "URL"
	Usage   string
	Choices []string // allowed values (validated, completed)
}

// Parsed holds the result of ParseFlags.
type Parsed struct {
	specs   map[string]*Flag
	vals    map[string]string
	Args    []string // positionals before "--"
	Rest    []string // everything after "--"
	HasRest bool
}

// ParseFlags parses args against specs. Flags and positionals may be interleaved;
// "--" ends flag parsing and the remainder is returned in Rest.
func ParseFlags(specs []Flag, args []string) (*Parsed, error) {
	p := &Parsed{specs: map[string]*Flag{}, vals: map[string]string{}}
	short := map[string]*Flag{}
	for i := range specs {
		f := &specs[i]
		p.specs[f.Name] = f
		if f.Short != "" {
			short[f.Short] = f
		}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			p.HasRest = true
			p.Rest = append([]string{}, args[i+1:]...)
			break
		}
		var f *Flag
		var val string
		var hasVal bool
		switch {
		case strings.HasPrefix(a, "--"):
			name := a[2:]
			if k := strings.IndexByte(name, '='); k >= 0 {
				name, val, hasVal = name[:k], name[k+1:], true
			}
			f = p.specs[name]
			if f == nil {
				return nil, fmt.Errorf("unknown flag --%s", name)
			}
		case len(a) >= 2 && a[0] == '-' && !isNumber(a):
			name := a[1:]
			if k := strings.IndexByte(name, '='); k >= 0 {
				name, val, hasVal = name[:k], name[k+1:], true
			}
			f = short[name]
			if f == nil {
				return nil, fmt.Errorf("unknown flag -%s", name)
			}
		default:
			p.Args = append(p.Args, a)
			continue
		}
		if f.Kind == Bool {
			if !hasVal {
				val = "true"
			} else if _, err := strconv.ParseBool(val); err != nil {
				return nil, fmt.Errorf("flag --%s: %q is not a boolean", f.Name, val)
			}
		} else if !hasVal {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("flag --%s needs a value", f.Name)
			}
			i++
			val = args[i]
		}
		if f.Kind == Int {
			if _, err := strconv.Atoi(val); err != nil {
				return nil, fmt.Errorf("flag --%s: %q is not an integer", f.Name, val)
			}
		}
		if len(f.Choices) > 0 && f.Kind == String && !inList(f.Choices, val) {
			return nil, fmt.Errorf("flag --%s: %q is not one of %s", f.Name, val, strings.Join(f.Choices, "|"))
		}
		p.vals[f.Name] = val
	}
	return p, nil
}

func isNumber(s string) bool { _, err := strconv.Atoi(s); return err == nil }

func inList(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

// Has reports whether the flag was given explicitly.
func (p *Parsed) Has(name string) bool { _, ok := p.vals[name]; return ok }

func (p *Parsed) raw(name string) string {
	if v, ok := p.vals[name]; ok {
		return v
	}
	if f := p.specs[name]; f != nil {
		return f.Default
	}
	return ""
}

// Bool returns a boolean flag.
func (p *Parsed) Bool(name string) bool { b, _ := strconv.ParseBool(p.raw(name)); return b }

// String returns a string flag (default if unset).
func (p *Parsed) String(name string) string { return p.raw(name) }

// Int returns an integer flag (default if unset).
func (p *Parsed) Int(name string) int { n, _ := strconv.Atoi(p.raw(name)); return n }

// List returns a comma-separated flag as a slice (empty entries dropped).
func (p *Parsed) List(name string) []string {
	var out []string
	for _, s := range strings.Split(p.raw(name), ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
