package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MQ37/nod/internal/cli"
	"github.com/MQ37/nod/internal/llamacpp"
)

func TestRegisteredCommandHelpAndInstallDryRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	getenv := func(k string) string { return os.Getenv(k) }
	mdir, lib := t.TempDir(), t.TempDir()
	for _, f := range []string{"decider.gguf", "tokenizer.json", "decider_config.json"} {
		if err := os.WriteFile(filepath.Join(mdir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(lib, llamacpp.LibName()), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	gguf := filepath.Join(mdir, "decider.gguf")
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"help", []string{"--help"}, "Commands:"},
		{"install plan", []string{"install", "--model", "fast", "--host", "none", "--dry-run", "--json", "--model-path", gguf, "--llama-lib-dir", lib}, "nothing is downloaded"},
		{"models list", []string{"models", "list", "--json"}, "convert_hf_to_gguf.py"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, stderr bytes.Buffer
			env := cli.Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &stderr, Getenv: getenv}
			if code := cli.Default.Run(context.Background(), env, tc.args); code != 0 {
				t.Fatalf("exit=%d stderr=%s", code, stderr.String())
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Fatalf("stdout missing %q: %s", tc.want, out.String())
			}
		})
	}
}
