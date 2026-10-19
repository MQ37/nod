// Package pi installs the Decider extension into Pi / Oh My Pi (omp): extension file + policy module.
// The extension runs inside the agent; there is no hook command.
package pi

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/MQ37/nod/internal/api"
	"github.com/MQ37/nod/internal/hosts"
	"github.com/MQ37/nod/internal/hosts/assets"
)

const Name = "pi"

// files maps embedded asset -> file name inside the extension directory (index.ts is what Pi loads).
var files = [][2]string{{"pi/extension.ts", "index.ts"}, {"pi/policy.mjs", "policy.mjs"}}

// Adapter implements hosts.Host.
type Adapter struct{ Env hosts.Env }

func New(env hosts.Env) *Adapter { return &Adapter{Env: env.Norm()} }

func (a *Adapter) Name() string { return Name }

// base is the agent directory: ~/.omp if present, else ~/.pi if present, else ~/.omp (the tested one).
func (a *Adapter) base() string {
	for _, d := range []string{".omp", ".pi"} {
		if st, err := os.Stat(a.Env.Join(d)); err == nil && st.IsDir() {
			return a.Env.Join(d)
		}
	}
	return a.Env.Join(".omp")
}

func (a *Adapter) extDir() string { return filepath.Join(a.base(), "agent", "extensions", "decider") }

func (a *Adapter) Detect() (hosts.Info, error) {
	info := hosts.Info{Name: Name, Config: a.extDir()}
	if st, err := os.Stat(a.base()); err == nil && st.IsDir() {
		info.Present, info.Path = true, a.base()
	}
	return info, nil
}

func (a *Adapter) PlanInstall(opts hosts.Options) (hosts.Plan, error) {
	plan := hosts.Plan{Host: Name}
	for _, f := range files {
		b, err := assets.FS.ReadFile(f[0])
		if err != nil {
			return plan, err
		}
		st, err := hosts.FileStep(filepath.Join(a.extDir(), f[1]), b, 0o644, true, "Pi extension file")
		if err != nil {
			return plan, err
		}
		plan.Steps = append(plan.Steps, st)
	}
	plan.Notes = append(plan.Notes,
		"the extension has no shadow mode: it blocks on `block`, asks in the UI on `ask`, and blocks on any error",
		"it calls the server's native /decide endpoint and defaults to a hard-coded host; set DECIDER_URL in the environment of the agent (a new session loads the extension; /reload in a running one)",
		"disable with DECIDER_PI=off, or `/decider off` inside a session")
	return plan, nil
}

func (a *Adapter) Install(plan hosts.Plan) error { return hosts.Apply(a.Env, plan) }

func (a *Adapter) ours() bool {
	b, _ := hosts.ReadFile(filepath.Join(a.extDir(), "index.ts"))
	return b != nil && bytes.Contains(b, []byte("Pi extension: score each tool call with Decider"))
}

func (a *Adapter) PlanRemove() (hosts.Plan, error) {
	plan := hosts.Plan{Host: Name}
	if !a.ours() {
		return plan, nil
	}
	for _, f := range files {
		st, err := hosts.DeleteStep(filepath.Join(a.extDir(), f[1]), "Pi extension file")
		if err != nil {
			return plan, err
		}
		plan.Steps = append(plan.Steps, st)
	}
	plan.Steps = append(plan.Steps, hosts.Step{Path: a.extDir(), Op: "rmdir"})
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
	s := hosts.Status{Host: Name, Supported: true, Config: a.extDir(), Mode: "enforce (no shadow mode)"}
	info, _ := a.Detect()
	s.Present = info.Present
	wired := true
	for _, f := range files {
		want, _ := assets.FS.ReadFile(f[0])
		got, _ := hosts.ReadFile(filepath.Join(a.extDir(), f[1]))
		if !bytes.Equal(got, want) {
			wired = false
		}
	}
	s.Wired = wired
	if !wired {
		s.Detail = "extension not installed (or differs from this nod version)"
	}
	if st, err := os.Stat(filepath.Join(a.Env.Home, ".local", "share", "nod", "omp-decisions.jsonl")); err == nil {
		s.LastVerdict = st.ModTime()
	}
	return s
}

// Hook: the extension runs in-process; there is nothing to answer on stdout.
func (a *Adapter) Hook(stdin io.Reader, stdout io.Writer, cl api.Classifier) int {
	fmt.Fprintln(a.Env.Stderr, "nod: Pi uses the in-process Decider extension; `hook pi` has nothing to do (run `nod hooks install --host pi`)")
	return 0
}
