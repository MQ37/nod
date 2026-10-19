package opencode

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MQ37/nod/internal/hosts"
	"github.com/MQ37/nod/internal/hosts/assets"
)

func newAdapter(t *testing.T, mode string, env map[string]string) (*Adapter, string) {
	t.Helper()
	home := t.TempDir()
	return New(hosts.Env{
		Home:       home,
		Getenv:     func(k string) string { return env[k] },
		Now:        func() time.Time { return time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC) },
		RuntimeDir: filepath.Join(home, "run"),
		Mode:       func() string { return mode },
		URL:        "http://127.0.0.1:11096",
		Stderr:     &bytes.Buffer{},
	}), home
}

func apply(t *testing.T, a *Adapter, p hosts.Plan, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if err = a.Install(p); err != nil {
		t.Fatal(err)
	}
}

func readSettings(t *testing.T, a *Adapter) Settings {
	t.Helper()
	b, err := os.ReadFile(a.settingsFile())
	if err != nil {
		t.Fatal(err)
	}
	var s Settings
	if err = json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestInstallStatusIdempotentRemove(t *testing.T) {
	a, home := newAdapter(t, "enforce", nil)
	if s := a.Status(); s.Wired || !s.Supported {
		t.Fatalf("fresh status: %#v", s)
	}
	p, err := a.PlanInstall(hosts.Options{})
	apply(t, a, p, err)
	if want := filepath.Join(home, ".config", "opencode", "plugin", "nod.js"); a.pluginFile() != want {
		t.Fatalf("plugin path %s, want %s", a.pluginFile(), want)
	}
	got, _ := os.ReadFile(a.pluginFile())
	want, _ := assets.FS.ReadFile(asset)
	if !bytes.Equal(got, want) || !bytes.HasPrefix(got, []byte(marker)) {
		t.Fatal("plugin file differs from the embedded asset or lacks the marker")
	}
	set := readSettings(t, a)
	if set.ManagedBy != "nod" || set.Mode != "enforce" || set.Review != "server" || set.URL != "http://127.0.0.1:11096" || set.TimeoutMS != 15000 || len(set.Tools) != 2 || set.Tools[0] != "bash" || set.Tools[1] != "shell" {
		t.Fatalf("settings: %#v", set)
	}
	if st, _ := os.Stat(a.settingsFile()); st.Mode().Perm() != 0o600 {
		t.Fatalf("nod.json mode %v", st.Mode().Perm())
	}
	s := a.Status()
	if !s.Present || !s.Wired || s.Mode != "enforce" || s.Detail != "review: server" {
		t.Fatalf("installed status: %#v", s)
	}
	// Idempotent: a second plan changes nothing and leaves no backups.
	p, err = a.PlanInstall(hosts.Options{})
	if err != nil || p.Changes() {
		t.Fatalf("second install plan changes something: %v %#v", err, p)
	}
	apply(t, a, p, nil)
	if m, _ := filepath.Glob(filepath.Join(a.pluginDir(), "*.bak-nod-*")); len(m) != 0 {
		t.Fatalf("unexpected backups: %v", m)
	}
	r, err := a.PlanRemove()
	apply(t, a, r, err)
	if _, err = os.Stat(a.pluginDir()); !os.IsNotExist(err) {
		t.Fatalf("plugin directory remains: %v", err)
	}
	if s = a.Status(); s.Wired {
		t.Fatalf("status after remove: %#v", s)
	}
	if r, _ = a.PlanRemove(); r.Changes() || len(r.Steps) != 0 {
		t.Fatalf("second remove plan: %#v", r)
	}
}

func TestAgentReviewAndModeSwitchBacksUpSettings(t *testing.T) {
	a, _ := newAdapter(t, "", nil)
	p, err := a.PlanInstall(hosts.Options{Review: "agent"})
	apply(t, a, p, err)
	if !strings.Contains(strings.Join(p.Notes, "\n"), "one extra request") {
		t.Fatalf("agent plan lacks the cost note: %v", p.Notes)
	}
	set := readSettings(t, a)
	if set.Review != "agent" || set.Mode != "shadow" || set.TimeoutMS != 60000 {
		t.Fatalf("agent settings: %#v", set)
	}
	if s := a.Status(); !s.Wired || s.Mode != "shadow" || s.Detail != "review: agent" {
		t.Fatalf("agent status: %#v", s)
	}
	// Re-install with the server review: nod.json changes with a backup, the plugin stays.
	p, err = a.PlanInstall(hosts.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if p.Steps[0].Op != "none" || p.Steps[1].Op != "modify" || !p.Steps[1].Backup {
		t.Fatalf("switch plan: %#v", p.Steps)
	}
	apply(t, a, p, nil)
	if readSettings(t, a).Review != "server" {
		t.Fatal("review not switched")
	}
	if m, _ := filepath.Glob(a.settingsFile() + ".bak-nod-*"); len(m) != 1 {
		t.Fatalf("expected one settings backup, got %v", m)
	}
}

func TestInstallBacksUpForeignFilesAndRemoveKeepsThem(t *testing.T) {
	a, _ := newAdapter(t, "shadow", nil)
	if err := os.MkdirAll(a.pluginDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(a.pluginDir(), "mine.ts")
	foreign := []byte("export default async () => ({})\n")
	for _, f := range []string{a.pluginFile(), other} {
		if err := os.WriteFile(f, foreign, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Remove never touches files nod did not write.
	if r, _ := a.PlanRemove(); len(r.Steps) != 0 {
		t.Fatalf("remove plans to touch foreign files: %#v", r.Steps)
	}
	p, err := a.PlanInstall(hosts.Options{})
	apply(t, a, p, err)
	bk, _ := filepath.Glob(a.pluginFile() + ".bak-nod-*")
	if len(bk) != 1 {
		t.Fatalf("expected a backup of the foreign nod.js, got %v", bk)
	}
	if b, _ := os.ReadFile(bk[0]); !bytes.Equal(b, foreign) {
		t.Fatal("backup content differs")
	}
	r, err := a.PlanRemove()
	apply(t, a, r, err)
	for _, f := range []string{other, bk[0]} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("%s removed: %v", f, err)
		}
	}
	for _, f := range []string{a.pluginFile(), a.settingsFile()} {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Fatalf("%s remains: %v", f, err)
		}
	}
	// A nod.json without nod's marker is not ours either.
	if err := os.WriteFile(a.settingsFile(), []byte(`{"url":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if r, _ = a.PlanRemove(); len(r.Steps) != 0 {
		t.Fatalf("remove plans to delete a foreign nod.json: %#v", r.Steps)
	}
}

func TestConfigDirOverridesAndHook(t *testing.T) {
	a, _ := newAdapter(t, "", map[string]string{"OPENCODE_CONFIG_DIR": "/cfg/oc", "XDG_CONFIG_HOME": "/xdg"})
	if a.pluginFile() != "/cfg/oc/plugin/nod.js" {
		t.Fatalf("OPENCODE_CONFIG_DIR ignored: %s", a.pluginFile())
	}
	a, _ = newAdapter(t, "", map[string]string{"XDG_CONFIG_HOME": "/xdg"})
	if a.pluginFile() != "/xdg/opencode/plugin/nod.js" {
		t.Fatalf("XDG_CONFIG_HOME ignored: %s", a.pluginFile())
	}
	var out bytes.Buffer
	if code := a.Hook(nil, &out, nil); code != 0 || out.Len() != 0 {
		t.Fatalf("hook: code %d stdout %q", code, out.String())
	}
}

func TestUsesExistingPluginsDirectory(t *testing.T) {
	a, home := newAdapter(t, "shadow", nil)
	dir := filepath.Join(home, ".config", "opencode", "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := a.PlanInstall(hosts.Options{})
	apply(t, a, p, err)
	if _, err := os.Stat(filepath.Join(dir, "nod.js")); err != nil {
		t.Fatalf("with an existing plugins/ directory nod must install there: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "plugin")); err == nil {
		t.Fatal("must not also create plugin/")
	}
}
