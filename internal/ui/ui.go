// Package ui is the small interactive layer of the CLI: yes/no, menus, text input,
// progress and a spinner. Hand-rolled on bufio + ANSI; colour only on a TTY and
// never when NO_COLOR is set. No TTY libraries.
package ui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrNonInteractive is returned by prompts when the UI is not interactive.
var ErrNonInteractive = errors.New("ui: not interactive")

// ColorEnabled decides colour: only for a TTY, never with NO_COLOR (any value) or TERM=dumb.
func ColorEnabled(isTTY bool, getenv func(string) string) bool {
	return isTTY && getenv("NO_COLOR") == "" && getenv("TERM") != "dumb"
}

// UI talks to the human on In/Out.
type UI struct {
	in          *bufio.Reader
	out         io.Writer
	color       bool
	interactive bool
}

// New builds a UI. interactive=false makes every prompt return ErrNonInteractive.
func New(in io.Reader, out io.Writer, color, interactive bool) *UI {
	if in == nil {
		in = strings.NewReader("")
	}
	return &UI{in: bufio.NewReader(in), out: out, color: color, interactive: interactive}
}

// Interactive reports whether prompts are allowed.
func (u *UI) Interactive() bool { return u.interactive }

func (u *UI) paint(code, s string) string {
	if !u.color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// Bold, Dim, Red, Green and Yellow colour a string when colour is enabled.
func (u *UI) Bold(s string) string   { return u.paint("1", s) }
func (u *UI) Dim(s string) string    { return u.paint("2", s) }
func (u *UI) Red(s string) string    { return u.paint("31", s) }
func (u *UI) Green(s string) string  { return u.paint("32", s) }
func (u *UI) Yellow(s string) string { return u.paint("33", s) }

func (u *UI) readLine() (string, error) {
	line, err := u.in.ReadString('\n')
	if err != nil && (err != io.EOF || line == "") {
		return "", fmt.Errorf("ui: input closed: %w", err)
	}
	return strings.TrimSpace(line), nil
}

// Confirm asks a yes/no question; Enter takes def.
func (u *UI) Confirm(q string, def bool) (bool, error) {
	if !u.interactive {
		return def, ErrNonInteractive
	}
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	for {
		fmt.Fprintf(u.out, "%s %s ", u.Bold(q), hint)
		s, err := u.readLine()
		if err != nil {
			return def, err
		}
		switch strings.ToLower(s) {
		case "":
			return def, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		fmt.Fprintln(u.out, "please answer y or n")
	}
}

// Choice is one menu entry.
type Choice struct {
	Value string
	Label string
}

// Choose shows a numbered menu and returns the index picked; Enter takes def.
// A choice can be given by number or by its Value.
func (u *UI) Choose(q string, choices []Choice, def int) (int, error) {
	if !u.interactive {
		return def, ErrNonInteractive
	}
	if len(choices) == 0 {
		return -1, errors.New("ui: no choices")
	}
	fmt.Fprintln(u.out, u.Bold(q))
	for i, c := range choices {
		mark := " "
		if i == def {
			mark = "*"
		}
		fmt.Fprintf(u.out, " %s %d) %s\n", mark, i+1, c.Label)
	}
	for {
		fmt.Fprintf(u.out, "choice [%d]: ", def+1)
		s, err := u.readLine()
		if err != nil {
			return def, err
		}
		if s == "" {
			return def, nil
		}
		if n, err := strconv.Atoi(s); err == nil && n >= 1 && n <= len(choices) {
			return n - 1, nil
		}
		for i, c := range choices {
			if strings.EqualFold(s, c.Value) {
				return i, nil
			}
		}
		fmt.Fprintf(u.out, "enter a number 1-%d\n", len(choices))
	}
}

// Input asks for a line of text; Enter takes def.
func (u *UI) Input(q, def string) (string, error) {
	if !u.interactive {
		return def, ErrNonInteractive
	}
	if def != "" {
		fmt.Fprintf(u.out, "%s [%s]: ", u.Bold(q), def)
	} else {
		fmt.Fprintf(u.out, "%s: ", u.Bold(q))
	}
	s, err := u.readLine()
	if err != nil {
		return def, err
	}
	if s == "" {
		return def, nil
	}
	return s, nil
}

// Progress renders a byte/step counter. On a colour/TTY UI it redraws one line with
// a bar; in plain mode it prints a line per 10% so logs stay readable.
type Progress struct {
	u        *UI
	label    string
	total    int64
	lastStep int
	done     bool
	mu       sync.Mutex
}

// NewProgress starts a progress display for label. total<=0 means unknown size.
func (u *UI) NewProgress(label string, total int64) *Progress {
	return &Progress{u: u, label: label, total: total, lastStep: -1}
}

// Update reports the current count.
func (p *Progress) Update(n int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done {
		return
	}
	if p.total <= 0 {
		return
	}
	pct := int(n * 100 / p.total)
	if pct > 100 {
		pct = 100
	}
	if p.u.color {
		const w = 24
		fill := pct * w / 100
		fmt.Fprintf(p.u.out, "\r%s [%s%s] %3d%%", p.label, strings.Repeat("#", fill), strings.Repeat("-", w-fill), pct)
		return
	}
	if step := pct / 10; step != p.lastStep {
		p.lastStep = step
		fmt.Fprintf(p.u.out, "%s: %d%%\n", p.label, step*10)
	}
}

// Done finishes the display.
func (p *Progress) Done() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done {
		return
	}
	p.done = true
	if p.u.color {
		fmt.Fprintf(p.u.out, "\r%s %s\n", p.label, p.u.Green("done")+strings.Repeat(" ", 30))
	} else if p.total <= 0 || p.lastStep < 10 {
		fmt.Fprintf(p.u.out, "%s: done\n", p.label)
	}
}

// Spin shows a spinner next to label until the returned stop function is called.
// Without colour/TTY it prints "label..." once.
func (u *UI) Spin(label string) (stop func(ok bool)) {
	if !u.color {
		fmt.Fprintf(u.out, "%s...\n", label)
		return func(bool) {}
	}
	quit := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		frames := []string{"|", "/", "-", "\\"}
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for i := 0; ; i++ {
			select {
			case <-quit:
				return
			case <-t.C:
				fmt.Fprintf(u.out, "\r%s %s", frames[i%4], label)
			}
		}
	}()
	var once sync.Once
	return func(ok bool) {
		once.Do(func() {
			close(quit)
			<-finished
			mark := u.Green("ok")
			if !ok {
				mark = u.Red("failed")
			}
			fmt.Fprintf(u.out, "\r%s %s\n", label, mark)
		})
	}
}
