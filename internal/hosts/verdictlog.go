package hosts

import (
	"bufio"
	"encoding/json"
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

// Verdict is one hook verdict. It never holds command text or conversation.
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
}

// VerdictLogPath is the shared CLI and hook decision log.
func (e Env) VerdictLogPath() string {
	return filepath.Join(e.Norm().RuntimeDir, "decisions.jsonl")
}

// LogVerdict appends one line best-effort; logging failure never changes a hook answer.
func (e Env) LogVerdict(v Verdict) {
	defer func() { _ = recover() }()
	e = e.Norm()
	ts := e.Now().UTC()
	if !v.Time.IsZero() {
		ts = v.Time.UTC()
	}
	entry := decisionlog.Entry{Time: ts, Host: v.Host, Tool: v.Tool, Decision: v.Decision, Source: v.Source, Rules: v.Rules, Ms: v.Ms}
	_ = decisionlog.New(e.VerdictLogPath(), false).Append(entry)
}

// LastVerdict returns the timestamp of the newest log line for host.
func (e Env) LastVerdict(host string) time.Time {
	f, err := os.Open(e.VerdictLogPath())
	if err != nil {
		return time.Time{}
	}
	defer f.Close()
	var last time.Time
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var v decisionlog.Entry
		if json.Unmarshal(sc.Bytes(), &v) == nil && v.Host == host && v.Time.After(last) {
			last = v.Time
		}
	}
	return last
}
