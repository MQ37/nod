// Package claude is the Claude Code adapter: it edits ~/.claude/settings.json (surgically, with a
// timestamped backup) and answers the PreToolUse hook.
package claude

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/MQ37/nod/internal/hosts"
)

// Name of the host.
const Name = "claude"

// ObserverEvents are the optional events recorded by `nod hook claude --observe`.
var ObserverEvents = []string{"PostToolUse", "PostToolUseFailure", "PermissionRequest", "PermissionDenied"}

// Adapter implements hosts.Host.
type Adapter struct{ Env hosts.Env }

// New returns the adapter.
func New(env hosts.Env) *Adapter { return &Adapter{Env: env.Norm()} }

func (a *Adapter) Name() string { return Name }

func (a *Adapter) dir() string { return a.Env.Join(".claude") }

// SettingsPath is the user-level settings file (a symlink is followed).
func (a *Adapter) SettingsPath() string {
	if d := a.Env.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return hosts.ResolveLink(filepath.Join(d, "settings.json"))
	}
	return hosts.ResolveLink(filepath.Join(a.dir(), "settings.json"))
}

func (a *Adapter) cmd(suffix string) string {
	c := a.Env.Command
	if strings.ContainsAny(c, " \t") {
		c = `"` + c + `"`
	}
	return c + " hook claude" + suffix
}

func hookGroup(command string, timeout int, matcher string) jv {
	return jobj{{"matcher", jstr(matcher)}, {"hooks", jarr{jobj{{"type", jstr("command")}, {"command", jstr(command)}, {"timeout", jnum(timeout)}}}}}
}

func (a *Adapter) Detect() (hosts.Info, error) {
	info := hosts.Info{Name: Name, Config: a.SettingsPath()}
	dir := a.dir()
	if d := a.Env.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		dir = d
	}
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		info.Present, info.Path = true, dir
	} else if p, err := exec.LookPath("claude"); err == nil {
		info.Present, info.Path = true, p
	}
	return info, nil
}

func (a *Adapter) settingsError(err error) error {
	var ue *userError
	if errors.As(err, &ue) {
		return hosts.Errf(hosts.ExitVerify, "%s %s", a.SettingsPath(), ue.msg)
	}
	return err
}

func (a *Adapter) plan(edit func([]byte) ([]byte, error), desc string) (hosts.Plan, error) {
	path := a.SettingsPath()
	plan := hosts.Plan{Host: Name}
	cur, err := hosts.ReadFile(path)
	if err != nil {
		return plan, err
	}
	next, err := edit(cur)
	if err != nil {
		return plan, a.settingsError(err)
	}
	step := hosts.Step{Path: path, Op: "none", Desc: desc, BaseSHA: hosts.SHA(cur), Mode: 0o600}
	if string(next) != string(cur) {
		step.Op, step.Content, step.Backup = "modify", next, cur != nil
		if cur == nil {
			step.Op = "create"
		}
		step.Diff = hosts.UnifiedDiff(path, string(cur), string(next))
	}
	plan.Steps = []hosts.Step{step}
	return plan, nil
}

func (a *Adapter) PlanInstall(opts hosts.Options) (hosts.Plan, error) {
	matcher := opts.Matcher
	if matcher == "" {
		matcher = "Bash"
	}
	plan, err := a.plan(func(b []byte) ([]byte, error) {
		b, err := ensure(b, "PreToolUse", hookGroup(a.cmd(""), 15, matcher), func(c string) bool { return isOurs(c) && !isObserve(c) })
		if err != nil || !opts.Observers {
			if err != nil {
				return b, err
			}
		} else {
			for _, ev := range ObserverEvents {
				if b, err = ensure(b, ev, hookGroup(a.cmd(" --observe"), 5, "*"), func(c string) bool { return isOurs(c) && isObserve(c) }); err != nil {
					return nil, err
				}
			}
		}
		if opts.Warm {
			b, err = ensure(b, "SessionStart", hookGroup(a.cmd(" --warm"), 15, "*"), func(c string) bool { return isWarm(c) })
			if err != nil {
				return nil, err
			}
		}
		return b, nil
	}, "add the nod PreToolUse hook")
	if err == nil {
		plan.Notes = append(plan.Notes, "mode is "+a.Env.CurrentMode()+" (shadow: verdicts are logged, Claude Code's own permission flow decides)")
	}
	return plan, err
}

func (a *Adapter) Install(plan hosts.Plan) error { return hosts.Apply(a.Env, plan) }

func (a *Adapter) PlanRemove() (hosts.Plan, error) {
	skipped := 0
	plan, err := a.plan(func(b []byte) ([]byte, error) {
		out, _, sk, err := removeOurs(b)
		skipped = sk
		return out, err
	}, "remove the nod hook entries")
	if err == nil && skipped > 0 {
		plan.Notes = append(plan.Notes, fmt.Sprintf("%d nod hook(s) share a hook group with another command and were left in place; remove them by hand", skipped))
	}
	return plan, err
}

func (a *Adapter) Remove() (hosts.Plan, error) {
	plan, err := a.PlanRemove()
	if err != nil {
		return plan, err
	}
	return plan, hosts.Apply(a.Env, plan)
}

func (a *Adapter) Status() hosts.Status {
	s := hosts.Status{Host: Name, Supported: true, Config: a.SettingsPath(), Mode: a.Env.CurrentMode()}
	info, _ := a.Detect()
	s.Present = info.Present
	b, err := hosts.ReadFile(s.Config)
	if err != nil {
		s.Detail = err.Error()
		return s
	}
	pre, obs, err := state(b)
	if err != nil {
		s.Detail = a.settingsError(err).Error()
		return s
	}
	s.Wired, s.Observers = pre, obs
	s.LastVerdict = a.Env.LastVerdict(Name)
	if !pre {
		s.Detail = "PreToolUse hook not present"
	}
	return s
}
