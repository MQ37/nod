// Package hermes installs the nod-gate plugin into Hermes Agent (plugin directory + env file lines).
// The plugin runs inside Hermes; there is no hook command.
package hermes

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/MQ37/nod/internal/api"
	"github.com/MQ37/nod/internal/hosts"
	"github.com/MQ37/nod/internal/hosts/assets"
)

const (
	Name        = "hermes"
	pluginName  = "nod-gate"
	beginMarker = "# nod begin (managed by `nod hooks`; remove with `nod hooks remove`)"
	endMarker   = "# nod end"
)

// Adapter implements hosts.Host.
type Adapter struct{ Env hosts.Env }

func New(env hosts.Env) *Adapter { return &Adapter{Env: env.Norm()} }

func (a *Adapter) Name() string { return Name }

func (a *Adapter) home() string {
	if h := a.Env.Getenv("HERMES_HOME"); h != "" {
		return h
	}
	return a.Env.Join(".hermes")
}

func (a *Adapter) pluginDir() string { return filepath.Join(a.home(), "plugins", pluginName) }
func (a *Adapter) envFile() string   { return filepath.Join(a.home(), ".env") }

func (a *Adapter) Detect() (hosts.Info, error) {
	info := hosts.Info{Name: Name, Config: a.envFile()}
	if st, err := os.Stat(a.home()); err == nil && st.IsDir() {
		info.Present, info.Path = true, a.home()
	}
	return info, nil
}

func pluginFiles() (map[string][]byte, error) {
	out := map[string][]byte{}
	root := "hermes/" + pluginName
	err := fs.WalkDir(assets.FS, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := assets.FS.ReadFile(p)
		out[strings.TrimPrefix(p, root+"/")] = b
		return err
	})
	return out, err
}

var keyRe = func(k string) *regexp.Regexp { return regexp.MustCompile(`(?m)^\s*(?:export\s+)?` + k + `\s*=`) }

// stripBlock removes our managed block, returns the rest.
func stripBlock(s string) (string, bool) {
	i := strings.Index(s, beginMarker)
	if i < 0 {
		return s, false
	}
	j := strings.Index(s[i:], endMarker)
	if j < 0 {
		return s, false
	}
	end := i + j + len(endMarker)
	if end < len(s) && s[end] == '\n' {
		end++
	}
	return s[:i] + s[end:], true
}

// reviewAgent is the hosts.Options.Review value that makes the plugin ask the session's own model.
const reviewAgent = "agent"

func (a *Adapter) envContent(cur, review string) string {
	rest, _ := stripBlock(cur)
	var lines []string
	add := func(k, v string) {
		if !keyRe(k).MatchString(rest) { // never overwrite the user's own lines
			lines = append(lines, k+"="+v)
		}
	}
	if a.Env.URL != "" {
		add("NOD_URL", a.Env.URL)
	}
	add("NOD_MODE", a.Env.CurrentMode())
	if review == reviewAgent {
		// the model call is bounded by NOD_TIMEOUT (plugin caps it at 20 s, below Hermes' 30 s hook timeout)
		add("NOD_REVIEW", reviewAgent)
		add("NOD_TIMEOUT", "15")
	} else {
		add("NOD_TIMEOUT", "2.5")
	}
	// the plugin writes its audit trail where nod reads it (nod audit verify, nod decisions)
	if a.Env.AuditDir != "" {
		add("NOD_AUDIT_DIR", a.Env.AuditDir)
	}
	if a.Env.AuditCommands != "" {
		add("NOD_AUDIT_COMMANDS", a.Env.AuditCommands)
	}
	if len(lines) == 0 {
		return rest
	}
	if rest != "" && !strings.HasSuffix(rest, "\n") {
		rest += "\n"
	}
	return rest + beginMarker + "\n" + strings.Join(lines, "\n") + "\n" + endMarker + "\n"
}

func (a *Adapter) PlanInstall(opts hosts.Options) (hosts.Plan, error) {
	plan := hosts.Plan{Host: Name}
	files, err := pluginFiles()
	if err != nil {
		return plan, err
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sortStrings(names)
	for _, n := range names {
		st, err := hosts.FileStep(filepath.Join(a.pluginDir(), n), files[n], 0o644, true, "Hermes plugin file")
		if err != nil {
			return plan, err
		}
		plan.Steps = append(plan.Steps, st)
	}
	cur, err := hosts.ReadFile(a.envFile())
	if err != nil {
		return plan, err
	}
	st, err := hosts.FileStep(a.envFile(), []byte(a.envContent(string(cur), opts.Review)), 0o600, true, "nod settings in the Hermes env file (existing lines are never overwritten)")
	if err != nil {
		return plan, err
	}
	plan.Steps = append(plan.Steps, st)
	if !a.enabled() {
		plan.Notes = append(plan.Notes, "enable the plugin once: `hermes plugins enable nod-gate` (plugins are opt-in), then restart Hermes")
	}
	return plan, nil
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func (a *Adapter) Install(plan hosts.Plan) error { return hosts.Apply(a.Env, plan) }

func (a *Adapter) ours() bool {
	b, _ := hosts.ReadFile(filepath.Join(a.pluginDir(), "plugin.yaml"))
	return b != nil && bytes.Contains(b, []byte("name: "+pluginName))
}

func (a *Adapter) PlanRemove() (hosts.Plan, error) {
	plan := hosts.Plan{Host: Name}
	if a.ours() {
		var dirs []string
		err := filepath.WalkDir(a.pluginDir(), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				dirs = append(dirs, p)
				return nil
			}
			st, err := hosts.DeleteStep(p, "Hermes plugin file")
			plan.Steps = append(plan.Steps, st)
			return err
		})
		if err != nil {
			return plan, err
		}
		for i := len(dirs) - 1; i >= 0; i-- {
			plan.Steps = append(plan.Steps, hosts.Step{Path: dirs[i], Op: "rmdir"})
		}
	}
	cur, err := hosts.ReadFile(a.envFile())
	if err != nil {
		return plan, err
	}
	if cur != nil {
		if rest, ok := stripBlock(string(cur)); ok {
			var st hosts.Step
			if strings.TrimSpace(rest) == "" {
				st, err = hosts.DeleteStep(a.envFile(), "remove the nod lines (file would be empty)")
			} else {
				st, err = hosts.FileStep(a.envFile(), []byte(rest), 0o600, true, "remove the nod lines from the Hermes env file")
			}
			if err != nil {
				return plan, err
			}
			if st.Op == "delete" {
				st.Backup = true
			}
			plan.Steps = append(plan.Steps, st)
		}
	}
	if a.enabled() {
		plan.Notes = append(plan.Notes, "also run `hermes plugins disable nod-gate` (or remove it from plugins.enabled in config.yaml)")
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

func (a *Adapter) enabled() bool {
	b, _ := hosts.ReadFile(filepath.Join(a.home(), "config.yaml"))
	return b != nil && bytes.Contains(b, []byte(pluginName))
}

func (a *Adapter) Status() hosts.Status {
	s := hosts.Status{Host: Name, Supported: true, Config: a.pluginDir()}
	info, _ := a.Detect()
	s.Present = info.Present
	files, err := pluginFiles()
	if err != nil {
		s.Detail = err.Error()
		return s
	}
	same := a.ours()
	for n, want := range files {
		got, _ := hosts.ReadFile(filepath.Join(a.pluginDir(), n))
		if !bytes.Equal(got, want) {
			same = false
		}
	}
	envb, _ := hosts.ReadFile(a.envFile())
	mode := "shadow (plugin default)"
	if m := regexp.MustCompile(`(?m)^\s*(?:export\s+)?NOD_MODE\s*=\s*(\S+)`).FindSubmatch(envb); m != nil {
		mode = strings.Trim(string(m[1]), `"'`)
	}
	if m := regexp.MustCompile(`(?m)^\s*(?:export\s+)?NOD_REVIEW\s*=\s*(\S+)`).FindSubmatch(envb); m != nil {
		mode += ", review=" + strings.Trim(string(m[1]), `"'`)
	} else {
		mode += ", review=decider"
	}
	s.Mode = mode
	switch {
	case !same:
		s.Detail = "plugin not installed (or differs from this nod version)"
	case !a.enabled():
		s.Detail = "plugin copied but not enabled: run `hermes plugins enable nod-gate`"
	default:
		s.Wired = true
	}
	s.LastVerdict = a.Env.LastVerdict(Name)
	return s
}

// Hook: Hermes loads the plugin in-process; there is nothing to answer on stdout.
func (a *Adapter) Hook(stdin io.Reader, stdout io.Writer, cl api.Classifier) int {
	fmt.Fprintln(a.Env.Stderr, "nod: Hermes uses the in-process plugin nod-gate; `hook hermes` has nothing to do (run `nod hooks install --host hermes`)")
	return 0
}
