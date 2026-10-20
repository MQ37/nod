// Package hooksintegration provides the CLI commands that connect host adapters
// to the nod classifier and install plans.
package hooksintegration

import (
	"fmt"
	"io"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/MQ37/nod/internal/api"
	"github.com/MQ37/nod/internal/cli"
	"github.com/MQ37/nod/internal/config"
	"github.com/MQ37/nod/internal/hosts"
	"github.com/MQ37/nod/internal/hosts/registry"
)

func init() {
	cli.Register("hook", cli.Command{Name: "hook", Summary: "answer one host hook event", Usage: "hook <claude|hermes|pi|opencode> [--warm|--observe]", Flags: []cli.Flag{{Name: "warm", Kind: cli.Bool, Usage: "warm the server at Claude SessionStart"}, {Name: "observe", Kind: cli.Bool, Usage: "record the raw hook event (Claude Code observer hook); classify nothing"}}, Run: runHook})
	cli.Register("hooks", cli.Command{Name: "hooks", Summary: "install, remove or inspect host hooks", Usage: "hooks <install|remove|status> [--host H] [--matcher TOOL] [--warm] [--observers] [--dry-run] [--yes]", Flags: []cli.Flag{{Name: "host", Kind: cli.String, Arg: "H", Usage: "comma-separated hosts"}, {Name: "matcher", Kind: cli.String, Default: "Bash", Arg: "TOOL", Usage: "Claude Code matcher"}, {Name: "warm", Kind: cli.Bool, Usage: "install Claude SessionStart model warm hook"}, {Name: "observers", Kind: cli.Bool, Usage: "also install the Claude Code observer hooks that record every raw event"}, {Name: "dry-run", Kind: cli.Bool, Usage: "print plan without applying"}}, Run: runHooks})
}

func paths(c *cli.Context) (config.Paths, config.Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return config.Paths{}, config.Config{}, err
	}
	uid := os.Getuid()
	if u, e := user.Current(); e == nil {
		fmt.Sscan(u.Uid, &uid)
	}
	p := config.ResolvePaths(c.Getenv, home, uid, c.G.Config)
	cfg, err := config.Load(p.ConfigFile)
	return p, cfg, err
}
func hostEnv(c *cli.Context, cfg config.Config, p config.Paths) hosts.Env {
	return hostEnvFor(c, cfg, p, "")
}

// hostEnvFor is hostEnv for one host: the Claude Code hook may run in its own mode (claude_mode), and
// with agent review the plugins talk to the local server, never to a remote that only serves Claude Code.
func hostEnvFor(c *cli.Context, cfg config.Config, p config.Paths, host string) hosts.Env {
	if host == "claude" && cfg.ClaudeMode != "" {
		cfg.Mode = cfg.ClaudeMode
	}
	url := cfg.ServerURL()
	if cfg.Model == "agent" {
		url = cfg.LocalURL()
	}
	h, _ := os.UserHomeDir()
	return hosts.Env{Home: h, Getenv: c.Getenv, Now: time.Now, RuntimeDir: p.RuntimeDir, AuditDir: p.Audit(cfg), AuditCommands: cfg.AuditCommands, AuditContext: cfg.AuditContext, EventsFile: p.Events(cfg), Mode: func() string { return cfg.Mode }, URL: url, OnError: cfg.OnUnreachable, Stderr: c.Stderr}
}
func classifier(cfg config.Config, p config.Paths) api.Classifier {
	return cli.NewClassifier(cfg, p)
}
func runHook(c *cli.Context) int {
	if len(c.Args) != 1 {
		c.Errorf("usage: nod hook <claude|hermes|pi|opencode>")
		return cli.ExitUsage
	}
	p, cfg, err := paths(c)
	if err != nil {
		c.Errorf("%v", err)
		return cli.ExitUsage
	}
	if c.Flags.Bool("warm") {
		if c.Args[0] != "claude" {
			c.Errorf("--warm is only supported for claude")
			return cli.ExitUsage
		}
		w, ok := classifier(cfg, p).(interface{ HealthWarm(bool) (string, error) })
		if !ok {
			c.Errorf("classifier does not support model warm requests")
			return cli.ExitUsage
		}
		_, err := w.HealthWarm(true)
		if err != nil {
			c.Errorf("model warm request failed: %v", err)
		}
		return cli.ExitOK
	}
	h, err := registry.Get(hostEnvFor(c, cfg, p, c.Args[0]), c.Args[0])
	if err != nil {
		c.Errorf("%v", err)
		return cli.ExitUsage
	}
	if c.Flags.Bool("observe") {
		o, ok := h.(interface{ Observe(io.Reader) int })
		if !ok {
			c.Errorf("--observe is only supported for claude")
			return cli.ExitUsage
		}
		return o.Observe(c.Stdin)
	}
	return h.Hook(c.Stdin, c.Stdout, classifier(cfg, p))
}
func runHooks(c *cli.Context) int {
	if len(c.Args) != 1 || !oneOf(c.Args[0], "install", "remove", "status") {
		c.Errorf("usage: nod hooks <install|remove|status> [--host H]")
		return cli.ExitUsage
	}
	p, cfg, err := paths(c)
	if err != nil {
		c.Errorf("%v", err)
		return cli.ExitUsage
	}
	names := c.Flags.String("host")
	var hs []hosts.Host
	if names == "" {
		if c.Args[0] == "install" {
			h, e := registry.Get(hostEnvFor(c, cfg, p, "claude"), "claude")
			if e != nil {
				c.Errorf("%v", e)
				return cli.ExitUsage
			}
			hs = []hosts.Host{h}
		} else {
			for _, n := range registry.Names {
				h, _ := registry.Get(hostEnvFor(c, cfg, p, n), n)
				hs = append(hs, h)
			}
		}
	} else {
		for _, n := range strings.Split(names, ",") {
			h, e := registry.Get(hostEnvFor(c, cfg, p, strings.TrimSpace(n)), strings.TrimSpace(n))
			if e != nil {
				c.Errorf("%v", e)
				return cli.ExitUsage
			}
			hs = append(hs, h)
		}
	}
	switch c.Args[0] {
	case "status":
		out := make([]hosts.Status, 0, len(hs))
		for _, h := range hs {
			out = append(out, h.Status())
		}
		if c.G.JSON {
			c.JSON(out)
		} else {
			for _, s := range out {
				fmt.Fprintf(c.Out(), "%-9s present=%t wired=%t mode=%s %s\n", s.Host, s.Present, s.Wired, s.Mode, s.Detail)
			}
		}
		return cli.ExitOK
	case "remove":
		plans := make([]hosts.Plan, 0, len(hs))
		for _, h := range hs {
			plan, e := h.PlanRemove()
			if e != nil {
				c.Errorf("%s: %v", h.Name(), e)
				return hosts.CodeOf(e)
			}
			plans = append(plans, plan)
		}
		printPlans(c, plans)
		if c.Flags.Bool("dry-run") {
			return cli.ExitOK
		}
		if !c.G.Yes {
			return c.Needs([]cli.Need{{Flag: "--yes", Question: "apply this host removal plan"}})
		}
		for i, h := range hs {
			if err := h.Install(plans[i]); err != nil {
				c.Errorf("%s: %v", h.Name(), err)
				return hosts.CodeOf(err)
			}
		}
		return cli.ExitOK
	case "install":
		opts := hosts.Options{Matcher: c.Flags.String("matcher"), Warm: c.Flags.Bool("warm"), Observers: c.Flags.Bool("observers")}
		if cfg.Model == "agent" {
			opts.Review = "agent"
		}
		plans := make([]hosts.Plan, 0, len(hs))
		for _, h := range hs {
			if cfg.Model == "agent" && h.Name() != "hermes" && h.Name() != "opencode" && !(h.Name() == "claude" && cfg.Remote != "") {
				c.Errorf("%s cannot use agent review (its hook cannot call the agent's model); use a Decider model for it (for claude, set a remote Decider with `nod config set remote URL`)", h.Name())
				return cli.ExitUsage
			}
			plan, e := h.PlanInstall(opts)
			if e != nil {
				c.Errorf("%s: %v", h.Name(), e)
				return hosts.CodeOf(e)
			}
			plans = append(plans, plan)
		}
		printPlans(c, plans)
		if c.Flags.Bool("dry-run") {
			return cli.ExitOK
		}
		if !c.G.Yes {
			return c.Needs([]cli.Need{{Flag: "--yes", Question: "apply this host configuration plan"}})
		}
		for i, h := range hs {
			if e := h.Install(plans[i]); e != nil {
				c.Errorf("%s: %v", h.Name(), e)
				return cli.ExitUsage
			}
		}
		return cli.ExitOK
	}
	return cli.ExitUsage
}
func printPlans(c *cli.Context, plans []hosts.Plan) {
	if c.G.JSON {
		c.JSON(plans)
		return
	}
	for _, p := range plans {
		fmt.Fprintf(c.Out(), "%s\n", p.Host)
		for _, s := range p.Steps {
			fmt.Fprintf(c.Out(), "  %s %s\n%s", s.Op, s.Path, s.Diff)
		}
		for _, n := range p.Notes {
			fmt.Fprintf(c.Out(), "  note: %s\n", n)
		}
	}
}
func oneOf(s string, vals ...string) bool {
	for _, v := range vals {
		if s == v {
			return true
		}
	}
	return false
}
