package hermes

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MQ37/nod/internal/hosts"
)

func TestInstallPreservesUserEnvAndRemove(t *testing.T) {
	home := t.TempDir()
	hdir := filepath.Join(home, ".hermes")
	if err := os.MkdirAll(hdir, 0700); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(hdir, ".env")
	original := []byte("CUSTOM=keep\nNOD_MODE=from-user\n")
	if err := os.WriteFile(envFile, original, 0600); err != nil {
		t.Fatal(err)
	}
	a := New(hosts.Env{Home: home, Getenv: func(string) string { return "" }, Now: func() time.Time { return time.Now() }, RuntimeDir: filepath.Join(home, "run"), URL: "http://127.0.0.1:11096", Stderr: &bytes.Buffer{}})
	p, err := a.PlanInstall(hosts.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Install(p); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(got, original) || strings.Contains(string(got), "NOD_MODE=enforce") {
		t.Fatalf("user env was changed: %s", got)
	}
	s := a.Status()
	if !s.Present || s.Wired {
		t.Fatalf("expected copied but disabled plugin status: %#v", s)
	}
	cfg := []byte("plugins:\n  enabled:\n    - nod-gate\n")
	if err = os.WriteFile(filepath.Join(hdir, "config.yaml"), cfg, 0600); err != nil {
		t.Fatal(err)
	}
	if s = a.Status(); !s.Wired {
		t.Fatalf("expected enabled plugin: %#v", s)
	}
	r, err := a.PlanRemove()
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Install(r); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("env remove round trip mismatch: %q", got)
	}
	if _, err = os.Stat(filepath.Join(hdir, "plugins", pluginName)); !os.IsNotExist(err) {
		t.Fatalf("plugin directory remains: %v", err)
	}
}

func newTestAdapter(t *testing.T) (*Adapter, string) {
	t.Helper()
	home := t.TempDir()
	hdir := filepath.Join(home, ".hermes")
	if err := os.MkdirAll(hdir, 0700); err != nil {
		t.Fatal(err)
	}
	a := New(hosts.Env{Home: home, Getenv: func(string) string { return "" }, Now: func() time.Time { return time.Now() }, RuntimeDir: filepath.Join(home, "run"), URL: "http://127.0.0.1:11096", Stderr: &bytes.Buffer{}})
	return a, filepath.Join(hdir, ".env")
}

func TestEnvLinesWithAndWithoutReview(t *testing.T) {
	a, _ := newTestAdapter(t)
	plain := a.envContent("CUSTOM=keep\n", "")
	if strings.Contains(plain, "NOD_REVIEW") || !strings.Contains(plain, "NOD_TIMEOUT=2.5\n") || !strings.Contains(plain, "NOD_URL=http://127.0.0.1:11096\n") {
		t.Fatalf("default install env: %q", plain)
	}
	agent := a.envContent("CUSTOM=keep\n", "agent")
	for _, want := range []string{"CUSTOM=keep\n", "NOD_URL=http://127.0.0.1:11096\n", "NOD_REVIEW=agent\n", "NOD_TIMEOUT=15\n", beginMarker, endMarker} {
		if !strings.Contains(agent, want) {
			t.Fatalf("agent install env lacks %q: %q", want, agent)
		}
	}
	if strings.Contains(agent, "NOD_TIMEOUT=2.5") {
		t.Fatalf("agent install kept the Decider timeout: %q", agent)
	}
	if again := a.envContent(agent, "agent"); again != agent {
		t.Fatalf("agent env not idempotent:\n%q\n%q", agent, again)
	}
	if back := a.envContent(agent, ""); back != plain {
		t.Fatalf("reinstall without review must drop NOD_REVIEW:\n%q\n%q", back, plain)
	}
	if user := a.envContent("NOD_REVIEW=off\n", "agent"); strings.Count(user, "NOD_REVIEW") != 1 {
		t.Fatalf("user's own NOD_REVIEW was overwritten: %q", user)
	}
}

func TestAgentReviewInstallStatusRemove(t *testing.T) {
	a, envFile := newTestAdapter(t)
	original := []byte("CUSTOM=keep\n")
	if err := os.WriteFile(envFile, original, 0600); err != nil {
		t.Fatal(err)
	}
	p, err := a.PlanInstall(hosts.Options{Review: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := a.PlanInstall(hosts.Options{Review: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Steps) != len(p2.Steps) {
		t.Fatalf("plan not deterministic")
	}
	for i := range p.Steps {
		if p.Steps[i].Path != p2.Steps[i].Path || p.Steps[i].Diff != p2.Steps[i].Diff {
			t.Fatalf("plan step %d not deterministic", i)
		}
	}
	if err = a.Install(p); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(envFile)
	if !strings.Contains(string(got), "\nNOD_REVIEW=agent\n") {
		t.Fatalf("env lacks NOD_REVIEW: %q", got)
	}
	if s := a.Status(); !strings.Contains(s.Mode, "review=agent") {
		t.Fatalf("status mode does not report review: %q", s.Mode)
	}
	r, err := a.PlanRemove()
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Install(r); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(envFile)
	if !bytes.Equal(got, original) {
		t.Fatalf("remove left nod lines: %q", got)
	}
	// a default install afterwards keeps working and writes no NOD_REVIEW
	p, err = a.PlanInstall(hosts.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Install(p); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(envFile)
	if strings.Contains(string(got), "NOD_REVIEW") {
		t.Fatalf("default install wrote NOD_REVIEW: %q", got)
	}
	if s := a.Status(); !strings.Contains(s.Mode, "review=decider") {
		t.Fatalf("status mode: %q", s.Mode)
	}
}

func TestPluginAssetsShipNoTests(t *testing.T) {
	files, err := pluginFiles()
	if err != nil {
		t.Fatal(err)
	}
	for n := range files {
		if strings.Contains(n, "test") {
			t.Fatalf("test file shipped inside the plugin: %s", n)
		}
	}
	for _, n := range []string{"__init__.py", "core.py", "audit.py", "plugin.yaml"} {
		if _, ok := files[n]; !ok {
			t.Fatalf("plugin file %s missing", n)
		}
	}
}

func TestAuditEnvLines(t *testing.T) {
	a, envFile := newTestAdapter(t)
	a.Env.AuditDir, a.Env.AuditCommands = "/state/nod/audit", "preview"
	got := a.envContent("CUSTOM=keep\n", "")
	for _, want := range []string{"\nNOD_AUDIT_DIR=/state/nod/audit\n", "\nNOD_AUDIT_COMMANDS=preview\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("env lacks %q: %q", want, got)
		}
	}
	if user := a.envContent("NOD_AUDIT_DIR=/mine\nNOD_AUDIT_COMMANDS=hash\n", ""); strings.Count(user, "NOD_AUDIT_DIR") != 1 || strings.Count(user, "NOD_AUDIT_COMMANDS") != 1 {
		t.Fatalf("user's own audit settings were overwritten: %q", user)
	}
	// without a level the plugin uses its default (full) and no line is written
	a.Env.AuditCommands = ""
	if got := a.envContent("", ""); strings.Contains(got, "NOD_AUDIT_COMMANDS") || !strings.Contains(got, "NOD_AUDIT_DIR=/state/nod/audit\n") {
		t.Fatalf("no level: %q", got)
	}
	// a default Env still resolves an audit dir (New normalises it), so the plugin and nod agree on the file
	if d := New(hosts.Env{Home: "/h", Getenv: func(string) string { return "" }}).envContent("", ""); !strings.Contains(d, "NOD_AUDIT_DIR=/h/.local/state/nod/audit\n") {
		t.Fatalf("default audit dir: %q", d)
	}

	original := []byte("CUSTOM=keep\n")
	if err := os.WriteFile(envFile, original, 0600); err != nil {
		t.Fatal(err)
	}
	a.Env.AuditCommands = "hash"
	p, err := a.PlanInstall(hosts.Options{})
	if err != nil {
		t.Fatal(err)
	}
	p2, _ := a.PlanInstall(hosts.Options{})
	for i := range p.Steps {
		if p.Steps[i].Path != p2.Steps[i].Path || p.Steps[i].Diff != p2.Steps[i].Diff {
			t.Fatalf("plan step %d not deterministic", i)
		}
	}
	if err = a.Install(p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(envFile)
	if !strings.Contains(string(b), "\nNOD_AUDIT_DIR=/state/nod/audit\nNOD_AUDIT_COMMANDS=hash\n"+endMarker) {
		t.Fatalf("installed env: %q", b)
	}
	if _, err := os.Stat(filepath.Join(a.pluginDir(), "audit.py")); err != nil {
		t.Fatalf("audit.py not installed: %v", err)
	}
	if s := a.Status(); s.Mode != "shadow, review=decider" {
		t.Fatalf("status mode changed: %q", s.Mode)
	}
	r, err := a.PlanRemove()
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Install(r); err != nil {
		t.Fatal(err)
	}
	if b, _ = os.ReadFile(envFile); !bytes.Equal(b, original) {
		t.Fatalf("remove left audit lines: %q", b)
	}
	if _, err := os.Stat(a.pluginDir()); !os.IsNotExist(err) {
		t.Fatalf("plugin dir (with audit.py) not removed: %v", err)
	}
}

func TestStatusLastVerdictFromAuditTrail(t *testing.T) {
	a, _ := newTestAdapter(t)
	a.Env.AuditDir = filepath.Join(t.TempDir(), "audit")
	if s := a.Status(); !s.LastVerdict.IsZero() {
		t.Fatalf("no trail yet: %v", s.LastVerdict)
	}
	a.Env.LogVerdict(hosts.Verdict{Host: Name, Tool: "terminal", Mode: "shadow", Decision: "unchecked"})
	if s := a.Status(); s.LastVerdict.IsZero() {
		t.Fatal("status does not read the hermes audit trail")
	}
}
