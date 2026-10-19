// Package opencode installs the nod plugin into OpenCode: <config dir>/plugin/nod.js (embedded) and
// its settings file nod.json next to it. OpenCode auto-loads every *.js / *.ts in plugin/ (or plugins/)
// of its global config dir at start; the plugin runs inside OpenCode, there is no hook command.
package opencode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/MQ37/nod/internal/api"
	"github.com/MQ37/nod/internal/decisionlog"
	"github.com/MQ37/nod/internal/hosts"
	"github.com/MQ37/nod/internal/hosts/assets"
)

const (
	Name        = "opencode"
	asset       = "opencode/nod.js"
	marker      = "// nod OpenCode plugin (managed by nod"
	managedBy   = "nod"
	reviewAgent = "agent"
)

// Settings is nod.json, read by the plugin at start.
type Settings struct {
	ManagedBy string   `json:"managed_by"`
	URL       string   `json:"url,omitempty"`
	Mode      string   `json:"mode"`
	Review    string   `json:"review"`
	TimeoutMS int      `json:"timeout_ms"`
	Tools     []string `json:"tools"`
	// The audit trail: the plugin appends to <AuditDir>/opencode.jsonl (the decisionlog format).
	AuditDir      string `json:"audit_dir,omitempty"`
	AuditCommands string `json:"audit_commands,omitempty"` // full | preview | hash
}

// Adapter implements hosts.Host.
type Adapter struct{ Env hosts.Env }

func New(env hosts.Env) *Adapter { return &Adapter{Env: env.Norm()} }

func (a *Adapter) Name() string { return Name }

// configDir mirrors OpenCode's global config dir: $OPENCODE_CONFIG_DIR, else $XDG_CONFIG_HOME/opencode, else ~/.config/opencode.
func (a *Adapter) configDir() string {
	if d := a.Env.Getenv("OPENCODE_CONFIG_DIR"); d != "" {
		return d
	}
	if x := a.Env.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "opencode")
	}
	return a.Env.Join(".config", "opencode")
}

// pluginDir is where opencode loads plugins from: it scans both plugin/ and plugins/. Use the
// directory that already holds nod's file, else an existing plugins/ (what people with other
// plugins have), else plugin/.
func (a *Adapter) pluginDir() string {
	for _, name := range []string{"plugin", "plugins"} {
		if _, err := os.Stat(filepath.Join(a.configDir(), name, "nod.js")); err == nil {
			return filepath.Join(a.configDir(), name)
		}
	}
	if st, err := os.Stat(filepath.Join(a.configDir(), "plugins")); err == nil && st.IsDir() {
		return filepath.Join(a.configDir(), "plugins")
	}
	return filepath.Join(a.configDir(), "plugin")
}
func (a *Adapter) pluginFile() string   { return filepath.Join(a.pluginDir(), "nod.js") }
func (a *Adapter) settingsFile() string { return filepath.Join(a.pluginDir(), "nod.json") }

func (a *Adapter) Detect() (hosts.Info, error) {
	info := hosts.Info{Name: Name, Config: a.pluginFile()}
	if st, err := os.Stat(a.configDir()); err == nil && st.IsDir() {
		info.Present, info.Path = true, a.configDir()
	} else if p, err := exec.LookPath("opencode"); err == nil {
		info.Present, info.Path = true, p
	}
	return info, nil
}

func agentReview(opts hosts.Options) bool {
	return strings.EqualFold(strings.TrimSpace(opts.Review), reviewAgent)
}

// auditCommands normalises the command text level like the plugin does: unknown or empty is full.
func auditCommands(v string) string {
	switch l := strings.ToLower(strings.TrimSpace(v)); l {
	case decisionlog.CommandsPreview, decisionlog.CommandsHash:
		return l
	}
	return decisionlog.CommandsFull
}

func (a *Adapter) settings(opts hosts.Options) []byte {
	s := Settings{ManagedBy: managedBy, URL: a.Env.URL, Mode: a.Env.CurrentMode(), Review: "server", TimeoutMS: 15000, Tools: []string{"bash", "shell"},
		AuditDir: a.Env.AuditDir, AuditCommands: auditCommands(a.Env.AuditCommands)}
	if agentReview(opts) {
		s.Review, s.TimeoutMS = reviewAgent, 60000 // a chat-model request takes longer than the Decider
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return append(b, '\n')
}

func (a *Adapter) PlanInstall(opts hosts.Options) (hosts.Plan, error) {
	plan := hosts.Plan{Host: Name}
	b, err := assets.FS.ReadFile(asset)
	if err != nil {
		return plan, err
	}
	st, err := hosts.FileStep(a.pluginFile(), b, 0o644, true, "OpenCode plugin file")
	if err != nil {
		return plan, err
	}
	plan.Steps = append(plan.Steps, st)
	st, err = hosts.FileStep(a.settingsFile(), a.settings(opts), 0o600, true, "nod settings for the OpenCode plugin")
	if err != nil {
		return plan, err
	}
	plan.Steps = append(plan.Steps, st)
	plan.Notes = append(plan.Notes,
		"restart OpenCode: it loads plugins at start; NOD_URL / NOD_MODE / NOD_REVIEW / NOD_AUDIT_COMMANDS in its environment override nod.json",
		"audit trail: every tool call OpenCode runs (gated or not) is appended to "+filepath.Join(a.Env.AuditDir, Name+".jsonl")+"; check it with `nod audit verify`",
		"enforce: allow runs the call; block and ask fail the tool call with a message (an OpenCode plugin cannot open an approval prompt, so ask tells the model to get the user's explicit confirmation first); any failure counts as ask")
	if agentReview(opts) {
		plan.Notes = append(plan.Notes, "review agent: every gated call costs one extra request to the session's own model, billed to the same account, in a hidden child session that is deleted afterwards")
	}
	return plan, nil
}

func (a *Adapter) Install(plan hosts.Plan) error { return hosts.Apply(a.Env, plan) }

func (a *Adapter) pluginOurs() bool {
	b, _ := hosts.ReadFile(a.pluginFile())
	return b != nil && bytes.HasPrefix(b, []byte(marker))
}

// readSettings returns nod.json when nod wrote it.
func (a *Adapter) readSettings() (Settings, bool) {
	var s Settings
	b, _ := hosts.ReadFile(a.settingsFile())
	if b == nil || json.Unmarshal(b, &s) != nil || s.ManagedBy != managedBy {
		return Settings{}, false
	}
	return s, true
}

func (a *Adapter) PlanRemove() (hosts.Plan, error) {
	plan := hosts.Plan{Host: Name}
	if a.pluginOurs() {
		st, err := hosts.DeleteStep(a.pluginFile(), "OpenCode plugin file")
		if err != nil {
			return plan, err
		}
		plan.Steps = append(plan.Steps, st)
	}
	if _, ok := a.readSettings(); ok {
		st, err := hosts.DeleteStep(a.settingsFile(), "nod settings for the OpenCode plugin")
		if err != nil {
			return plan, err
		}
		plan.Steps = append(plan.Steps, st)
	}
	if len(plan.Steps) > 0 {
		plan.Steps = append(plan.Steps, hosts.Step{Path: a.pluginDir(), Op: "rmdir"}) // only removed when empty
		plan.Notes = append(plan.Notes, "restart OpenCode to unload the plugin")
	}
	return plan, nil
}

func (a *Adapter) Remove() (hosts.Plan, error) {
	plan, err := a.PlanRemove()
	if err != nil {
		return plan, err
	}
	return plan, hosts.Apply(a.Env, plan)
}

func (a *Adapter) Status() hosts.Status {
	s := hosts.Status{Host: Name, Supported: true, Config: a.pluginFile()}
	info, _ := a.Detect()
	s.Present = info.Present
	want, _ := assets.FS.ReadFile(asset)
	got, _ := hosts.ReadFile(a.pluginFile())
	set, ok := a.readSettings()
	switch {
	case !bytes.Equal(got, want):
		s.Detail = "plugin not installed (or differs from this nod version)"
	case !ok:
		s.Detail = "plugin installed but nod.json is missing or not nod's"
	default:
		s.Wired = true
		s.Mode = hosts.ModeShadow
		if set.Mode == hosts.ModeEnforce {
			s.Mode = hosts.ModeEnforce
		}
		review := "server"
		if set.Review == reviewAgent {
			review = reviewAgent
		}
		s.Detail = "review: " + review
	}
	// The plugin appends to the audit dir written in nod.json (the same dir as Env.AuditDir when nod wrote it).
	auditEnv := a.Env
	if ok && set.AuditDir != "" && filepath.IsAbs(set.AuditDir) {
		auditEnv.AuditDir = set.AuditDir
	}
	s.LastVerdict = auditEnv.LastVerdict(Name)
	return s
}

// Hook: OpenCode loads the plugin in-process; there is nothing to answer on stdout.
func (a *Adapter) Hook(stdin io.Reader, stdout io.Writer, cl api.Classifier) int {
	fmt.Fprintln(a.Env.Stderr, "nod: OpenCode uses the in-process plugin nod.js; `hook opencode` has nothing to do (run `nod hooks install --host opencode`)")
	return 0
}
