// Package hosts defines the adapter interface for the agents nod gates (Claude Code, Hermes, Pi, opencode)
// and the shared plumbing: plans with diffs, atomic apply with backups, the verdict log.
// Sub-packages implement Host; internal/hosts/registry lists them (it imports them, so they cannot import it).
package hosts

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MQ37/nod/internal/api"
)

// Modes.
const (
	ModeShadow  = "shadow"  // log verdicts only, the host's own permission flow is unchanged
	ModeEnforce = "enforce" // answer allow / ask / deny
)

// Exit codes (SPEC section 1).
const (
	ExitUsage    = 1
	ExitNeeds    = 5
	ExitVerify   = 4
	ExitNotFound = 3
)

// ExitError is an error that carries the process exit code the CLI should use.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

// CodeOf returns the exit code for err (ExitUsage for other errors).
func CodeOf(err error) int {
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return ExitUsage
}

// ErrNotSupported is returned by hosts that cannot be wired yet.
var ErrNotSupported = errors.New("host not supported yet")

// Env is everything an adapter needs from the machine; tests set it to a temp HOME.
type Env struct {
	Home          string              // user's home directory
	Getenv        func(string) string // environment lookup (os.Getenv by default)
	Now           func() time.Time    // clock (backups, logs)
	RuntimeDir    string              // RAM-only scratch directory ($XDG_RUNTIME_DIR/nod)
	AuditDir      string              // persistent audit trail directory; "" = $NOD_AUDIT_DIR or $XDG_STATE_HOME/nod/audit
	EventsFile    string              // where the Claude Code observer hook appends raw hook events
	AuditContext  int                 // previous user messages stored with a verdict (Claude Code hook); 0 = none
	AuditCommands string              // what the audit trail stores of a command: full (default) | preview | hash
	Mode          func() string       // current mode: "shadow" | "enforce" (stored in the nod config)
	URL           string              // server URL written to host config (Hermes env file); "" = host default
	OnError       string              // "ask" (default) | "block" | "allow": answer when the classifier fails
	Command       string              // how hosts invoke us, default "nod"
	Stderr        io.Writer           // diagnostics (never stdout)
}

// DefaultEnv builds the Env for the running process.
func DefaultEnv() Env {
	home, _ := os.UserHomeDir()
	e := Env{Home: home, Getenv: os.Getenv, Now: time.Now, Stderr: os.Stderr}
	return e.withDefaults()
}

func (e Env) withDefaults() Env {
	if e.Getenv == nil {
		e.Getenv = os.Getenv
	}
	if e.Now == nil {
		e.Now = time.Now
	}
	if e.Stderr == nil {
		e.Stderr = io.Discard
	}
	if e.Home == "" {
		e.Home, _ = os.UserHomeDir()
	}
	if e.RuntimeDir == "" {
		e.RuntimeDir = defaultRuntimeDir(e.Getenv)
	}
	if e.AuditDir == "" {
		e.AuditDir = defaultAuditDir(e.Getenv, e.Home)
	}
	if e.Command == "" {
		e.Command = "nod"
	}
	return e
}

// Norm returns e with defaults filled in.
func (e Env) Norm() Env { return e.withDefaults() }

// CurrentMode is the mode in effect, "shadow" unless explicitly "enforce".
func (e Env) CurrentMode() string {
	if e.Mode != nil && strings.EqualFold(strings.TrimSpace(e.Mode()), ModeEnforce) {
		return ModeEnforce
	}
	return ModeShadow
}

// FailDecision is the decision used when the classifier cannot answer.
func (e Env) FailDecision() api.Decision {
	switch strings.ToLower(e.OnError) {
	case "block":
		return api.Block
	case "allow":
		return api.Allow
	}
	return api.Ask
}

// Options of PlanInstall.
type Options struct {
	Observers bool   // Claude Code: also install the observer events
	Matcher   string // Claude Code PreToolUse matcher; defaults to Bash
	Warm      bool   // Claude Code: load the model at SessionStart
	// Review "agent": the host plugin judges calls with the model the agent is already running
	// (nod install --model agent) instead of asking the Decider. Empty: ask the Decider server.
	Review string
}

// Info is the result of Detect.
type Info struct {
	Name    string `json:"name"`
	Present bool   `json:"present"` // the host looks installed on this machine
	Path    string `json:"path,omitempty"`
	Config  string `json:"config,omitempty"` // file the adapter edits
	Note    string `json:"note,omitempty"`
}

// Step is one file change of a plan.
type Step struct {
	Path    string      `json:"path"`
	Op      string      `json:"op"` // create | modify | delete | none
	Desc    string      `json:"desc,omitempty"`
	Diff    string      `json:"diff,omitempty"` // unified diff (text files)
	Content []byte      `json:"-"`
	Mode    os.FileMode `json:"-"`
	Backup  bool        `json:"backup,omitempty"` // write a timestamped backup of the existing file first
	BaseSHA string      `json:"-"`                // sha256 of the file when planned ("" = absent); Apply refuses if it changed
}

// Plan is what Install or Remove will do.
type Plan struct {
	Host  string   `json:"host"`
	Steps []Step   `json:"steps"`
	Notes []string `json:"notes,omitempty"` // things the user must do by hand
}

// Changes reports whether the plan changes anything.
func (p Plan) Changes() bool {
	for _, s := range p.Steps {
		if s.Op != "none" && s.Op != "" {
			return true
		}
	}
	return false
}

// Status of one host.
type Status struct {
	Host        string    `json:"host"`
	Supported   bool      `json:"supported"`
	Present     bool      `json:"present"` // host found on this machine
	Wired       bool      `json:"wired"`   // nod hook/plugin is in place
	Observers   bool      `json:"observers,omitempty"`
	Mode        string    `json:"mode,omitempty"`
	LastVerdict time.Time `json:"last_verdict,omitempty"`
	Config      string    `json:"config,omitempty"`
	Detail      string    `json:"detail,omitempty"` // reason / problem, human readable
}

// Host is implemented by every adapter.
type Host interface {
	Name() string
	Detect() (Info, error)
	PlanInstall(opts Options) (Plan, error)
	Install(plan Plan) error
	PlanRemove() (Plan, error)
	Remove() (Plan, error) // applies PlanRemove and returns what it did
	Status() Status
	// Hook answers one host event: stdin is the host's JSON, stdout gets ONLY the host's answer.
	// On any internal error it answers ask (or the configured policy); the return value is the process exit code.
	Hook(stdin io.Reader, stdout io.Writer, cl api.Classifier) int
}

// Hostname helper for messages.
func Errf(code int, format string, a ...any) error {
	return &ExitError{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// Join is filepath.Join on the env's home.
func (e Env) Join(parts ...string) string {
	return filepath.Join(append([]string{e.Home}, parts...)...)
}
