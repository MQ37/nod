package skill

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	skillfile "github.com/MQ37/nod/skill"
)

func run(c Command, args ...string) (int, string, string) {
	var o, e bytes.Buffer
	code := c.Run(args, IO{Stdin: strings.NewReader(""), Stdout: &o, Stderr: &e})
	return code, o.String(), e.String()
}

func TestPrint(t *testing.T) {
	code, out, _ := run(Command{}, "print")
	if code != 0 || out != skillfile.Markdown || !strings.HasPrefix(out, "---\nname: nod\n") {
		t.Fatalf("code=%d, frontmatter wrong", code)
	}
}

func TestInstall(t *testing.T) {
	home := t.TempDir()
	c := Command{Home: home}
	path := filepath.Join(home, ".claude", "skills", "nod", "SKILL.md")
	if code, out, _ := run(c, "install"); code != 0 || !strings.HasPrefix(out, "created "+path) {
		t.Fatalf("first install: %d %q", code, out)
	}
	if _, out, _ := run(c, "install"); !strings.HasPrefix(out, "unchanged") {
		t.Fatalf("second install: %q", out)
	}
	os.WriteFile(path, []byte("old"), 0o644)
	if _, out, _ := run(c, "install"); !strings.HasPrefix(out, "updated") {
		t.Fatalf("update: %q", out)
	}
	if b, _ := os.ReadFile(path + ".bak"); string(b) != "old" {
		t.Fatal("backup missing")
	}
}

func TestInstallDirAndUsage(t *testing.T) {
	d := t.TempDir()
	if code, _, _ := run(Command{}, "install", "--dir", d, "--json"); code != 0 {
		t.Fatal(code)
	}
	if _, err := os.Stat(filepath.Join(d, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := run(Command{}); code != 1 {
		t.Fatal("no args must be usage")
	}
	if code, _, _ := run(Command{}, "bogus"); code != 1 {
		t.Fatal("unknown must be usage")
	}
}
