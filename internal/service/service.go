// Package service installs and drives the nod daemon as a user service:
// a systemd --user unit on Linux, a launchd LaunchAgent on macOS. No root.
package service

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MQ37/nod/internal/config"
)

// Label and UnitName identify the default user service.
const (
	Label       = "io.github.mq37.nod"
	UnitName    = "nod.service"
	DefaultName = "nod"
)

// Spec describes the daemon to run.
type Spec struct {
	Binary string            // absolute path of the nod binary
	Args   []string          // arguments, e.g. ["serve", "--addr", "127.0.0.1:11096"]
	Env    map[string]string // extra environment (no secrets)
	LogDir string            // launchd stdout/stderr dir (systemd logs to the journal)
}

// Runner executes a system command (systemctl, launchctl); injectable.
type Runner interface {
	Run(name string, args ...string) (string, error)
}

// ExecRunner runs real commands.
type ExecRunner struct{}

// Run implements Runner.
func (ExecRunner) Run(name string, args ...string) (string, error) {
	var out bytes.Buffer
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

// Status of the installed service.
type Status struct {
	Installed bool   `json:"installed"`
	Active    bool   `json:"active"`
	Detail    string `json:"detail,omitempty"`
}

// Manager controls the service.
type Manager interface {
	Kind() string // "systemd" | "launchd"
	UnitPath() string
	LinkPath() string
	Render(Spec) string
	Install(Spec) error // write/link the unit, reload and enable it
	Uninstall() error   // stop, disable, remove the unit
	Start() error
	Stop() error
	Restart() error
	Status() Status
}

// ErrUnsupported is returned on platforms without a supported service manager.
var ErrUnsupported = errors.New("no supported service manager on this OS (need Linux systemd or macOS launchd); run `nod serve` yourself")

// New returns the Manager for goos.
func New(goos, home string, uid int, r Runner) (Manager, error) {
	return NewNamed(goos, home, uid, DefaultName, r)
}

// NewNamed returns a manager using a validated independent service identity.
func NewNamed(goos, home string, uid int, name string, r Runner) (Manager, error) {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" || !filepath.IsAbs(configHome) {
		configHome = filepath.Join(home, ".config")
	}
	return NewNamedAt(goos, home, configHome, uid, name, r)
}

// NewNamedAt returns a manager with explicit XDG config home and identity.
func NewNamedAt(goos, home, configHome string, uid int, name string, r Runner) (Manager, error) {
	if err := config.ValidateServiceName(name); err != nil {
		return nil, err
	}
	if configHome == "" || !filepath.IsAbs(configHome) {
		return nil, errors.New("XDG config home must be an absolute path")
	}
	switch goos {
	case "linux":
		return &systemd{home: home, configHome: configHome, name: name, r: r}, nil
	case "darwin":
		return &launchd{home: home, configHome: configHome, uid: uid, name: name, r: r}, nil
	}
	return nil, ErrUnsupported
}

// ---- systemd ----

func systemdQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'\\$%;") {
		return s
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `$$`, `%`, `%%`)
	return `"` + r.Replace(s) + `"`
}

// SystemdUnit renders the unit file.
func SystemdUnit(s Spec) string {
	var b strings.Builder
	b.WriteString("[Unit]\nDescription=nod: gate for AI agent tool calls\nAfter=network.target\n\n[Service]\nType=simple\n")
	parts := []string{systemdQuote(s.Binary)}
	for _, a := range s.Args {
		parts = append(parts, systemdQuote(a))
	}
	fmt.Fprintf(&b, "ExecStart=%s\n", strings.Join(parts, " "))
	for _, k := range sortedKeys(s.Env) {
		fmt.Fprintf(&b, "Environment=%s\n", systemdQuote(k+"="+s.Env[k]))
	}
	b.WriteString("Restart=always\nRestartSec=2\nNoNewPrivileges=yes\nPrivateTmp=yes\n\n[Install]\nWantedBy=default.target\n")
	return b.String()
}

func sortedKeys(m map[string]string) []string {
	k := make([]string, 0, len(m))
	for n := range m {
		k = append(k, n)
	}
	sort.Strings(k)
	return k
}

type systemd struct {
	home       string
	configHome string
	name       string
	r          Runner
}

func (d *systemd) Kind() string { return "systemd" }
func (d *systemd) UnitPath() string {
	return filepath.Join(d.configHome, "systemd", "user", d.unitName())
}

func (d *systemd) LinkPath() string {
	return filepath.Join(d.home, ".config", "systemd", "user", d.unitName())
}

func (d *systemd) unitName() string     { return d.name + ".service" }
func (d *systemd) Render(s Spec) string { return SystemdUnit(s) }
func (d *systemd) ctl(args ...string) (string, error) {
	return d.r.Run("systemctl", append([]string{"--user"}, args...)...)
}

func (d *systemd) Install(s Spec) error {
	if err := config.WriteFileAtomic(d.UnitPath(), []byte(SystemdUnit(s)), 0o644); err != nil {
		return err
	}
	if filepath.Clean(d.LinkPath()) != filepath.Clean(d.UnitPath()) {
		if err := os.MkdirAll(filepath.Dir(d.LinkPath()), 0o700); err != nil {
			return err
		}
		if target, err := os.Readlink(d.LinkPath()); err == nil {
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(d.LinkPath()), target)
			}
			if filepath.Clean(target) != filepath.Clean(d.UnitPath()) {
				return fmt.Errorf("systemd unit link already exists at %s", d.LinkPath())
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		} else if _, err := os.Lstat(d.LinkPath()); err == nil {
			return fmt.Errorf("systemd unit path already exists at %s", d.LinkPath())
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		} else if out, err := d.ctl("link", d.UnitPath()); err != nil {
			return fmt.Errorf("systemctl --user link %s: %v: %s", d.UnitPath(), err, out)
		}
	}
	if out, err := d.ctl("daemon-reload"); err != nil {
		return fmt.Errorf("systemctl --user daemon-reload: %v: %s", err, out)
	}
	if out, err := d.ctl("enable", "--now", d.unitName()); err != nil {
		return fmt.Errorf("systemctl --user enable --now: %v: %s", err, out)
	}
	return nil
}

func (d *systemd) Uninstall() error {
	if out, err := d.ctl("disable", "--now", d.unitName()); err != nil {
		return fmt.Errorf("systemctl --user disable --now %s: %v: %s", d.unitName(), err, out)
	}
	if filepath.Clean(d.LinkPath()) != filepath.Clean(d.UnitPath()) {
		if target, err := os.Readlink(d.LinkPath()); err == nil {
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(d.LinkPath()), target)
			}
			if filepath.Clean(target) != filepath.Clean(d.UnitPath()) {
				return fmt.Errorf("refusing to remove unrelated systemd unit link at %s", d.LinkPath())
			}
			if err := os.Remove(d.LinkPath()); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Remove(d.UnitPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, _ = d.ctl("daemon-reload")
	_, _ = d.ctl("reset-failed", d.unitName()) // a stop that interrupted a request must not leave a failed ghost
	return nil
}

func (d *systemd) run(verb string) error {
	if out, err := d.ctl(verb, d.unitName()); err != nil {
		return fmt.Errorf("systemctl --user %s %s: %v: %s", verb, d.unitName(), err, out)
	}
	return nil
}
func (d *systemd) Start() error   { return d.run("start") }
func (d *systemd) Stop() error    { return d.run("stop") }
func (d *systemd) Restart() error { return d.run("restart") }

func (d *systemd) Status() Status {
	st := Status{}
	if _, err := os.Stat(d.UnitPath()); err == nil {
		st.Installed = true
	} else if _, err := os.Lstat(d.LinkPath()); err == nil {
		st.Installed = true
	}
	out, _ := d.ctl("is-active", d.unitName())
	st.Detail = out
	st.Active = out == "active"
	return st
}

// ---- launchd ----

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

// LaunchdPlist renders the LaunchAgent plist.
func LaunchdPlist(s Spec) string {
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\" \"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n<plist version=\"1.0\">\n<dict>\n")
	fmt.Fprintf(&b, "\t<key>Label</key>\n\t<string>%s</string>\n", Label)
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, a := range append([]string{s.Binary}, s.Args...) {
		fmt.Fprintf(&b, "\t\t<string>%s</string>\n", xmlEscape(a))
	}
	b.WriteString("\t</array>\n")
	if len(s.Env) > 0 {
		b.WriteString("\t<key>EnvironmentVariables</key>\n\t<dict>\n")
		for _, k := range sortedKeys(s.Env) {
			fmt.Fprintf(&b, "\t\t<key>%s</key>\n\t\t<string>%s</string>\n", xmlEscape(k), xmlEscape(s.Env[k]))
		}
		b.WriteString("\t</dict>\n")
	}
	b.WriteString("\t<key>RunAtLoad</key>\n\t<true/>\n\t<key>KeepAlive</key>\n\t<true/>\n\t<key>ThrottleInterval</key>\n\t<integer>2</integer>\n")
	if s.LogDir != "" {
		fmt.Fprintf(&b, "\t<key>StandardOutPath</key>\n\t<string>%s</string>\n\t<key>StandardErrorPath</key>\n\t<string>%s</string>\n",
			xmlEscape(filepath.Join(s.LogDir, "nod.log")), xmlEscape(filepath.Join(s.LogDir, "nod.log")))
	}
	b.WriteString("</dict>\n</plist>\n")
	return b.String()
}

type launchd struct {
	home       string
	configHome string
	uid        int
	name       string
	r          Runner
}

func (d *launchd) Kind() string { return "launchd" }
func (d *launchd) UnitPath() string {
	return filepath.Join(d.home, "Library", "LaunchAgents", d.label()+".plist")
}
func (d *launchd) LinkPath() string { return d.UnitPath() }
func (d *launchd) label() string {
	if d.name == DefaultName {
		return Label
	}
	return Label + "." + d.name
}
func (d *launchd) Render(s Spec) string { return strings.ReplaceAll(LaunchdPlist(s), Label, d.label()) }
func (d *launchd) domain() string       { return fmt.Sprintf("gui/%d", d.uid) }
func (d *launchd) target() string       { return d.domain() + "/" + d.label() }

func (d *launchd) Install(s Spec) error {
	if s.LogDir != "" {
		if err := os.MkdirAll(s.LogDir, 0o700); err != nil {
			return err
		}
	}
	return config.WriteFileAtomic(d.UnitPath(), []byte(d.Render(s)), 0o644)
}

func (d *launchd) Uninstall() error {
	_, _ = d.r.Run("launchctl", "bootout", d.target())
	if err := os.Remove(d.UnitPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (d *launchd) Start() error {
	if out, err := d.r.Run("launchctl", "bootstrap", d.domain(), d.UnitPath()); err != nil && !strings.Contains(out, "already") && !strings.Contains(out, "Input/output error") {
		return fmt.Errorf("launchctl bootstrap: %v: %s", err, out)
	}
	if out, err := d.r.Run("launchctl", "kickstart", d.target()); err != nil {
		return fmt.Errorf("launchctl kickstart: %v: %s", err, out)
	}
	return nil
}

func (d *launchd) Stop() error {
	if out, err := d.r.Run("launchctl", "bootout", d.target()); err != nil {
		return fmt.Errorf("launchctl bootout: %v: %s", err, out)
	}
	return nil
}

func (d *launchd) Restart() error {
	if _, err := d.r.Run("launchctl", "kickstart", "-k", d.target()); err != nil {
		return d.Start()
	}
	return nil
}

func (d *launchd) Status() Status {
	st := Status{}
	if _, err := os.Stat(d.UnitPath()); err == nil {
		st.Installed = true
	}
	out, err := d.r.Run("launchctl", "print", d.target())
	st.Active = err == nil && strings.Contains(out, "state = running")
	if err != nil {
		st.Detail = "not loaded"
	} else if st.Active {
		st.Detail = "running"
	} else {
		st.Detail = "loaded"
	}
	return st
}
