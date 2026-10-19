package hosts

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/MQ37/nod/internal/decisionlog"
)

func defaultRuntimeDir(getenv func(string) string) string {
	if x := getenv("XDG_RUNTIME_DIR"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, "nod")
	}
	if st, err := os.Stat("/dev/shm"); err == nil && st.IsDir() {
		return filepath.Join("/dev/shm", fmt.Sprintf("nod-%d", os.Getuid()))
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("nod-%d", os.Getuid()))
}

func defaultAuditDir(getenv func(string) string, home string) string {
	if d := getenv("NOD_AUDIT_DIR"); d != "" && filepath.IsAbs(d) {
		return d
	}
	if x := getenv("XDG_STATE_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, "nod", "audit")
	}
	return filepath.Join(home, ".local", "state", "nod", "audit")
}

// Verdict is one hook verdict. Command is the original command text: it is redacted and clipped before
// anything is written (see decisionlog.CommandFields). No conversation text is ever stored.
type Verdict struct {
	Time     time.Time `json:"time"`
	Host     string    `json:"host"`
	Tool     string    `json:"tool,omitempty"`
	Mode     string    `json:"mode"`
	Decision string    `json:"decision,omitempty"`
	Source   string    `json:"source,omitempty"`
	Rules    []string  `json:"rules,omitempty"`
	Answer   string    `json:"answer,omitempty"`
	Ms       int       `json:"ms,omitempty"`
	Error    string    `json:"error,omitempty"`
	Command  string    `json:"-"`
	Session  string    `json:"-"` // host session id; stored as a short hash
	Review   string    `json:"-"`
	Category string    `json:"-"`
	Reason   string    `json:"-"`
	Cwd      string    `json:"-"`
}

// AuditFile is the audit log of one host: <audit dir>/<host>.jsonl.
func (e Env) AuditFile(host string) string {
	return filepath.Join(e.Norm().AuditDir, host+".jsonl")
}

// LogVerdict appends one audit line best-effort; logging failure never changes a hook answer.
func (e Env) LogVerdict(v Verdict) {
	defer func() { _ = recover() }()
	e = e.Norm()
	ts := e.Now().UTC()
	if !v.Time.IsZero() {
		ts = v.Time.UTC()
	}
	sha, text := decisionlog.CommandFields(v.Command, e.AuditCommands)
	entry := decisionlog.Entry{Time: ts, Host: v.Host, Session: decisionlog.HashSession(v.Session), Mode: v.Mode, Review: v.Review, Tool: v.Tool,
		Command: text, CommandSHA256: sha, Decision: v.Decision, Source: v.Source, Rules: v.Rules, Category: v.Category, Ms: v.Ms, Error: v.Error, Cwd: v.Cwd, Reason: v.Reason}
	_ = decisionlog.New(e.AuditFile(v.Host), false).Append(entry)
}

// LastVerdict returns the timestamp of the newest audit line for host.
func (e Env) LastVerdict(host string) time.Time {
	es, err := decisionlog.ReadLast(e.AuditFile(host), 1)
	if err != nil || len(es) == 0 {
		return time.Time{}
	}
	return es[len(es)-1].Time
}
