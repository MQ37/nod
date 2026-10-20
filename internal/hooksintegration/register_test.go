package hooksintegration_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MQ37/nod/internal/cli"
	_ "github.com/MQ37/nod/internal/hooksintegration"
)

func TestHooksInstallClaudeWithAgentModeNeedsARemoteDecider(t *testing.T) {
	home := t.TempDir()
	env := map[string]string{"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, "config"), "XDG_DATA_HOME": filepath.Join(home, "data"), "XDG_STATE_HOME": filepath.Join(home, "state"), "XDG_RUNTIME_DIR": filepath.Join(home, "run")}
	run := func(args ...string) (int, string) {
		var out, er bytes.Buffer
		code := cli.Default.Run(context.Background(), cli.Env{Stdout: &out, Stderr: &er, Getenv: func(k string) string { return env[k] }}, args)
		return code, er.String()
	}
	cfgDir := filepath.Join(home, "config", "nod")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"model":"agent"}`)
	if code, er := run("hooks", "install", "--host", "claude", "--dry-run", "--yes"); code != cli.ExitUsage || !strings.Contains(er, "remote") {
		t.Fatalf("no remote: exit=%d stderr=%q", code, er)
	}
	write(`{"model":"agent","remote":"http://127.0.0.1:1","claude_mode":"shadow"}`)
	if code, er := run("hooks", "install", "--host", "claude", "--dry-run", "--yes"); code != 0 {
		t.Fatalf("with remote: exit=%d stderr=%q", code, er)
	}
	if code, _ := run("config", "set", "claude_mode", "bogus"); code == 0 {
		t.Fatal("claude_mode must be validated")
	}
}
