package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MQ37/nod/internal/llamacpp"
)

// runDefault runs the real command table against a throwaway home so nothing touches the host.
func runDefault(t *testing.T, cfgJSON string, args ...string) (int, string, string) {
	t.Helper()
	home := t.TempDir()
	if cfgJSON != "" {
		dir := filepath.Join(home, "config", "nod")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfgJSON), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, "config"), "XDG_DATA_HOME": filepath.Join(home, "data"), "XDG_STATE_HOME": filepath.Join(home, "state"), "XDG_RUNTIME_DIR": filepath.Join(home, "run")}
	var out, er bytes.Buffer
	code := Default.Run(context.Background(), Env{Stdout: &out, Stderr: &er, Getenv: func(k string) string { return env[k] }}, args)
	return code, out.String(), er.String()
}

func TestCheckWithoutCommandIsUsageNotAsk(t *testing.T) {
	code, _, _ := runDefault(t, "", "check")
	if code != ExitUsage || code == 1 {
		t.Fatalf("exit=%d, want the usage code %d (1 means ask)", code, ExitUsage)
	}
}

func TestCheckUnreachableAnswersAskWithOwnSource(t *testing.T) {
	code, out, er := runDefault(t, `{"remote":"http://127.0.0.1:1","on_unreachable":"ask"}`, "check", "--json", "--", "ls")
	if code != 1 {
		t.Fatalf("exit=%d, want 1 (ask)", code)
	}
	var res struct{ Decision, Source, Error string }
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("json: %v (%q)", err, out)
	}
	if res.Decision != "ask" || res.Source != "unreachable" || res.Error == "" {
		t.Fatalf("got %+v", res)
	}
	if !strings.Contains(er, "unreachable") || strings.Contains(er, "127.0.0.1") {
		t.Fatalf("stderr should say unreachable without leaking the URL: %q", er)
	}
}

func TestInstallDownloadsNothingAndAsksForThePaths(t *testing.T) {
	code, out, _ := runDefault(t, "", "install", "--model", "fast", "--host", "none", "--dry-run", "--yes", "--json")
	if code != ExitNeeds {
		t.Fatalf("exit=%d, want %d: install must ask for the model and libraries instead of fetching them", code, ExitNeeds)
	}
	var res struct {
		Needs []struct{ Flag string } `json:"needs"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("json: %v (%q)", err, out)
	}
	got := map[string]bool{}
	for _, n := range res.Needs {
		got[n.Flag] = true
	}
	if !got["--model-path"] || !got["--llama-lib-dir"] {
		t.Fatalf("needs=%v, want both --model-path and --llama-lib-dir", res.Needs)
	}
}

func TestInstallPlanUsesProvidedFilesAndListsNoDownloads(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"decider.gguf", "tokenizer.json", "decider_config.json"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lib := t.TempDir()
	if err := os.WriteFile(filepath.Join(lib, llamacpp.LibName()), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, out, er := runDefault(t, "", "install", "--model", "fast", "--host", "none", "--dry-run", "--yes", "--json",
		"--model-path", filepath.Join(dir, "decider.gguf"), "--llama-lib-dir", lib)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, er)
	}
	if strings.Contains(out, "download") && strings.Contains(out, "http") {
		t.Fatalf("the plan must not contain downloads: %s", out)
	}
	if !strings.Contains(out, "model_path") || !strings.Contains(out, filepath.Join(dir, "decider.gguf")) {
		t.Fatalf("the config template must record the model path: %s", out)
	}
}

func TestInstallLiteNeedsItsChatSidecar(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"decider.gguf", "tokenizer.json", "decider_config.json"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lib := t.TempDir()
	if err := os.WriteFile(filepath.Join(lib, llamacpp.LibName()), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, _, er := runDefault(t, "", "install", "--model", "lite", "--host", "none", "--dry-run", "--yes", "--model-path", filepath.Join(dir, "decider.gguf"), "--llama-lib-dir", lib)
	if code != ExitNeeds || !strings.Contains(er, "nod models sidecar lite") {
		t.Fatalf("exit=%d stderr=%q", code, er)
	}
}

func TestInstallAgentModeNeedsNoModelAndRejectsHostsThatCannotCallIt(t *testing.T) {
	code, out, er := runDefault(t, "", "install", "--model", "agent", "--host", "none", "--dry-run", "--yes", "--json")
	if code != 0 {
		t.Fatalf("agent mode needs no model or libraries: exit=%d stderr=%q", code, er)
	}
	if !strings.Contains(out, "reviews each call") || strings.Contains(out, "model_path\": \"/") {
		t.Fatalf("plan: %s", out)
	}
	for _, host := range []string{"claude", "pi"} {
		code, _, er := runDefault(t, "", "install", "--model", "agent", "--host", host, "--dry-run", "--yes")
		if code != ExitUsage || !strings.Contains(er, "hermes, opencode") {
			t.Fatalf("%s: exit=%d stderr=%q", host, code, er)
		}
	}
}

func TestAuditTrailEndToEnd(t *testing.T) {
	home := t.TempDir()
	auditDir := filepath.Join(home, "state", "nod", "audit")
	env := map[string]string{"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, "config"), "XDG_DATA_HOME": filepath.Join(home, "data"), "XDG_STATE_HOME": filepath.Join(home, "state"), "XDG_RUNTIME_DIR": filepath.Join(home, "run")}
	run := func(args ...string) (int, string, string) {
		var out, er bytes.Buffer
		code := Default.Run(context.Background(), Env{Stdout: &out, Stderr: &er, Getenv: func(k string) string { return env[k] }}, args)
		return code, out.String(), er.String()
	}
	// A check with an unreachable server still leaves an audit line: every action is recorded.
	cfg := filepath.Join(home, "config", "nod")
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "config.json"), []byte(`{"remote":"http://127.0.0.1:1","remote_fallback":"none"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"ls -la", "rm -rf /tmp/x"} {
		if code, _, _ := run("check", "--", cmd); code != 1 {
			t.Fatalf("check exit=%d", code)
		}
	}
	if _, err := os.Stat(filepath.Join(auditDir, "check.jsonl")); err != nil {
		t.Fatalf("audit file missing: %v", err)
	}
	code, out, _ := run("decisions", "-v")
	if code != 0 || !strings.Contains(out, "ls -la") || !strings.Contains(out, "rm -rf /tmp/x") || !strings.Contains(out, "check") {
		t.Fatalf("decisions -v must show host and command: %q", out)
	}
	if code, out, _ = run("audit", "verify"); code != 0 || !strings.Contains(out, "chain intact") {
		t.Fatalf("verify exit=%d out=%q", code, out)
	}
	// Tamper with a line: verify must fail with exit 4.
	b, _ := os.ReadFile(filepath.Join(auditDir, "check.jsonl"))
	if err := os.WriteFile(filepath.Join(auditDir, "check.jsonl"), []byte(strings.Replace(string(b), "ls -la", "ls -l ", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, _ = run("audit", "verify"); code != ExitVerify || !strings.Contains(out, "BROKEN") {
		t.Fatalf("tampered log: exit=%d out=%q", code, out)
	}
	if code, out, _ = run("audit", "path"); code != 0 || strings.TrimSpace(out) != auditDir {
		t.Fatalf("audit path: %q", out)
	}
}
