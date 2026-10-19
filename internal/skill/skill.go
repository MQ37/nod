// Package skill implements `nod skill print|install [--dir DIR]`.
// The skill text is embedded from /skill/SKILL.md. Stdlib only.
package skill

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	skillfile "github.com/MQ37/nod/skill"
)

// IO carries the streams of a command run (injected for tests).
type IO struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
}

// Command is `nod skill`.
type Command struct {
	// Home overrides the home directory (tests); empty means os.UserHomeDir.
	Home string
}

// Other hosts' skill locations, printed after an install. The skill is a plain markdown file; these hosts read it as an instruction file.
var otherHosts = []string{
	"Hermes: copy it to ~/.hermes/skills/nod/SKILL.md",
	"Pi / Oh My Pi: copy it to ~/.omp/agent/skills/nod/SKILL.md (or ~/.pi/agent/skills/nod/SKILL.md)",
	"any other agent: nod skill print > its instruction file",
}

const usage = `usage: nod skill print
       nod skill install [--dir DIR] [--json]

print     write the agent skill (SKILL.md) to stdout
install   write it to ~/.claude/skills/nod/SKILL.md, or to DIR/SKILL.md with --dir

examples:
  nod skill print
  nod skill install
  nod skill install --dir ~/.config/my-agent/skills/nod
`

// Run executes the command and returns the process exit code (0 ok, 1 usage or write error).
func (c Command) Run(args []string, io IO) int {
	if len(args) == 0 {
		fmt.Fprint(io.Stderr, usage)
		return 1
	}
	switch args[0] {
	case "print":
		fmt.Fprint(io.Stdout, skillfile.Markdown)
		return 0
	case "install":
		return c.install(args[1:], io)
	case "-h", "--help", "help":
		fmt.Fprint(io.Stdout, usage)
		return 0
	}
	fmt.Fprintf(io.Stderr, "nod skill: unknown subcommand %q\n\n%s", args[0], usage)
	return 1
}

func (c Command) install(args []string, io IO) int {
	fs := flag.NewFlagSet("skill install", flag.ContinueOnError)
	fs.SetOutput(io.Stderr)
	dir := fs.String("dir", "", "target directory")
	asJSON := fs.Bool("json", false, "machine output")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	target := *dir
	if target == "" {
		home := c.Home
		if home == "" {
			h, err := os.UserHomeDir()
			if err != nil {
				fmt.Fprintln(io.Stderr, "nod skill: cannot find home directory:", err)
				return 1
			}
			home = h
		}
		target = filepath.Join(home, ".claude", "skills", "nod")
	}
	path := filepath.Join(target, "SKILL.md")
	status, err := write(path, skillfile.Markdown)
	if err != nil {
		fmt.Fprintln(io.Stderr, "nod skill:", err)
		return 1
	}
	if *asJSON {
		json.NewEncoder(io.Stdout).Encode(map[string]any{"path": path, "status": status})
		return 0
	}
	fmt.Fprintf(io.Stdout, "%s %s\n", status, path)
	if *dir == "" {
		for _, h := range otherHosts {
			fmt.Fprintln(io.Stdout, "  "+h)
		}
	}
	return 0
}

// write installs content at path; status is "created", "updated" or "unchanged". An old different file is kept as path.bak.
func write(path, content string) (string, error) {
	old, err := os.ReadFile(path)
	status := "created"
	switch {
	case err == nil && string(old) == content:
		return "unchanged", nil
	case err == nil:
		status = "updated"
		if err := os.WriteFile(path+".bak", old, 0o644); err != nil {
			return "", err
		}
	case !errors.Is(err, os.ErrNotExist):
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return status, nil
}
