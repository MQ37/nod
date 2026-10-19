package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func testEnv() (Env, *bytes.Buffer, *bytes.Buffer) {
	out, errout := new(bytes.Buffer), new(bytes.Buffer)
	e := Env{Stdin: strings.NewReader(""), Stdout: out, Stderr: errout, Getenv: func(string) string { return "" }, StdinTTY: true, StdoutTTY: true}
	return e, out, errout
}

func TestParseFlagsCases(t *testing.T) {
	fs := []Flag{{Name: "yes", Short: "y", Kind: Bool}, {Name: "model", Kind: String, Choices: []string{"full", "fast"}}}
	for _, tc := range []struct {
		name      string
		args      []string
		wantModel string
		wantRest  []string
		wantErr   bool
	}{
		{"long bool and value", []string{"--yes", "--model=fast"}, "fast", nil, false},
		{"short bool and terminator", []string{"-y", "--", "rm", "-rf"}, "", []string{"rm", "-rf"}, false},
		{"invalid choice", []string{"--model", "slow"}, "", nil, true},
		{"unknown flag", []string{"--bad"}, "", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := ParseFlags(fs, tc.args)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v", err)
			}
			if err == nil {
				if p.String("model") != tc.wantModel {
					t.Fatalf("model=%q", p.String("model"))
				}
				if strings.Join(p.Rest, " ") != strings.Join(tc.wantRest, " ") {
					t.Fatalf("rest=%v", p.Rest)
				}
			}
		})
	}
}

func TestDispatcherAndHelp(t *testing.T) {
	a := NewApp()
	a.Register("version", Command{Summary: "version", Run: func(c *Context) int { c.Printf("ok\n"); return 0 }})
	for _, tc := range []struct {
		name           string
		args           []string
		code           int
		stdout, stderr string
	}{
		{"dispatch", []string{"version"}, 0, "ok\n", ""},
		{"command help", []string{"version", "--help"}, 0, "nod version:", ""},
		{"unknown", []string{"versoin"}, ExitUsage, "", "unknown command"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, out, er := testEnv()
			code := a.Run(context.Background(), e, tc.args)
			if code != tc.code || !strings.Contains(out.String(), tc.stdout) || !strings.Contains(er.String(), tc.stderr) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), er.String())
			}
		})
	}
}

func TestCompletionShells(t *testing.T) {
	a := NewApp()
	a.Register("check", Command{Summary: "check", Flags: []Flag{{Name: "tool", Kind: String}}, Subs: []string{"--"}})
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			s, err := a.Completion(shell)
			if err != nil || !strings.Contains(s, "nod") {
				t.Fatalf("script=%q err=%v", s, err)
			}
		})
	}
	if _, err := a.Completion("powershell"); err == nil {
		t.Fatal("expected unsupported shell")
	}
}
