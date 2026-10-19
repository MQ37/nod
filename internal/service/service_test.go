package service

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls [][]string
	out   string
	err   error
}

func (f *fakeRunner) Run(name string, args ...string) (string, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	return f.out, f.err
}

func TestUnitRendering(t *testing.T) {
	unit := SystemdUnit(Spec{Binary: "/opt/tool guard", Args: []string{"serve", "--addr", "127.0.0.1:11096"}, Env: map[string]string{"B": "two words", "A": "x"}})
	want := "[Unit]\nDescription=nod: gate for AI agent tool calls\nAfter=network.target\n\n[Service]\nType=simple\nExecStart=\"/opt/tool guard\" serve --addr 127.0.0.1:11096\nEnvironment=A=x\nEnvironment=\"B=two words\"\nRestart=always\nRestartSec=2\nNoNewPrivileges=yes\nPrivateTmp=yes\n\n[Install]\nWantedBy=default.target\n"
	if unit != want {
		t.Fatalf("unit mismatch\n%s", unit)
	}
	plist := LaunchdPlist(Spec{Binary: "/opt/nod", Args: []string{"serve", "--addr", "a&b"}})
	if !strings.Contains(plist, "a&amp;b") {
		t.Fatalf("plist did not XML-escape arguments: %s", plist)
	}
}

func TestSystemdInstallUsesInjectedRunner(t *testing.T) {
	home := t.TempDir()
	runner := &fakeRunner{}
	m, err := NewNamedAt("linux", home, filepath.Join(home, ".config"), 123, DefaultName, runner)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Install(Spec{Binary: "/nod", Args: []string{"serve"}}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".config/systemd/user", UnitName))
	if err != nil || !strings.Contains(string(b), "ExecStart=/nod serve") {
		t.Fatalf("unit=%q err=%v", b, err)
	}
	want := [][]string{{"systemctl", "--user", "daemon-reload"}, {"systemctl", "--user", "enable", "--now", UnitName}}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls=%v want=%v", runner.calls, want)
	}
}

func TestSystemdLinksExternalXDGUnitAndUnlinksOnRemoval(t *testing.T) {
	home := t.TempDir()
	configHome := filepath.Join(home, "scratch-config")
	runner := &fakeRunner{}
	m, err := NewNamedAt("linux", home, configHome, 123, "nod-sandbox", runner)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Install(Spec{Binary: "/nod", Args: []string{"serve"}}); err != nil {
		t.Fatal(err)
	}
	if m.UnitPath() == m.LinkPath() {
		t.Fatal("external XDG unit path unexpectedly equals systemd search path")
	}
	if err := os.Symlink(m.UnitPath(), m.LinkPath()); err != nil {
		t.Fatal(err)
	}
	if err := m.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(m.LinkPath()); !os.IsNotExist(err) {
		t.Fatalf("unit link remains: %v", err)
	}
	if _, err := os.Stat(m.UnitPath()); !os.IsNotExist(err) {
		t.Fatalf("unit file remains: %v", err)
	}
	want := [][]string{
		{"systemctl", "--user", "link", m.UnitPath()},
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", "--now", "nod-sandbox.service"},
		{"systemctl", "--user", "disable", "--now", "nod-sandbox.service"},
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "reset-failed", "nod-sandbox.service"},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls=%v want=%v", runner.calls, want)
	}
}

func TestNamedServicesCoexist(t *testing.T) {
	home := t.TempDir()
	configHome := filepath.Join(home, "xdg-config")
	runner := &fakeRunner{}
	for _, name := range []string{"nod", "nod-sandbox"} {
		m, err := NewNamedAt("linux", home, configHome, 123, name, runner)
		if err != nil {
			t.Fatal(err)
		}
		if err := m.Install(Spec{Binary: "/nod", Args: []string{"serve"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(m.UnitPath()); err != nil {
			t.Fatalf("unit for %s: %v", name, err)
		}
	}
	for _, name := range []string{"nod.service", "nod-sandbox.service"} {
		if _, err := os.Stat(filepath.Join(configHome, "systemd", "user", name)); err != nil {
			t.Fatalf("coexisting unit %s missing: %v", name, err)
		}
	}
	if !strings.Contains((&launchd{name: "nod-sandbox"}).Render(Spec{Binary: "/nod"}), Label+".nod-sandbox") {
		t.Fatal("named launchd plist does not include the selected label")
	}
}
