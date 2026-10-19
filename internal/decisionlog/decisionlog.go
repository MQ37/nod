// Package decisionlog is the append-only verdict log (JSONL), kept in RAM
// ($XDG_RUNTIME_DIR/nod/decisions.jsonl). Lines carry verdicts only; command
// text is never persisted. Mode 0600, size cap with
// one rotated file (.1).
package decisionlog

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DefaultMaxBytes is the size at which the log rotates.
const DefaultMaxBytes = 1 << 20

// Entry is one verdict line.
type Entry struct {
	Time     time.Time `json:"time"`
	Tool     string    `json:"tool"`
	Decision string    `json:"decision"`
	Mode     string    `json:"mode,omitempty"`
	Source   string    `json:"source,omitempty"`
	Rules    []string  `json:"rules,omitempty"`
	Answer   string    `json:"answer,omitempty"`
	Category string    `json:"category,omitempty"`
	Ms       int       `json:"ms,omitempty"`
	Host     string    `json:"host,omitempty"` // which adapter asked: check, claude, hermes, ...
	Error    string    `json:"error,omitempty"`
	Cwd      string    `json:"cwd,omitempty"`
	Reason   string    `json:"reason,omitempty"`
	Command  string    `json:"command,omitempty"` // only when the writer is verbose
}

// Writer appends entries. Safe for concurrent use within one process; across
// processes each Append is a single O_APPEND write.
type Writer struct {
	Path     string
	MaxBytes int64
	Verbose  bool // retained for API compatibility; commands are never persisted
	mu       sync.Mutex
}

// New returns a writer for path.
func New(path string, verbose bool) *Writer {
	return &Writer{Path: path, MaxBytes: DefaultMaxBytes, Verbose: verbose}
}

// Append writes one entry. Errors are returned; callers on the hot path may ignore them.
func (w *Writer) Append(e Entry) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	e.Command = ""
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if err := os.MkdirAll(filepath.Dir(w.Path), 0o700); err != nil {
		return err
	}
	if w.MaxBytes > 0 {
		if fi, err := os.Stat(w.Path); err == nil && fi.Size()+int64(len(line)) > w.MaxBytes {
			_ = os.Rename(w.Path, w.Path+".1")
		}
	}
	f, err := os.OpenFile(w.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(line); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ReadLast returns up to n most recent entries (oldest first), reading the rotated
// file too when needed. Unparseable lines are skipped. A missing log is empty.
func ReadLast(path string, n int) ([]Entry, error) {
	var all []Entry
	for _, p := range []string{path + ".1", path} {
		es, err := readAll(p)
		if err != nil {
			return nil, err
		}
		all = append(all, es...)
	}
	if n > 0 && len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}

func readAll(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// Size returns the current size of the log file (0 when missing).
func Size(path string) int64 {
	if fi, err := os.Stat(path); err == nil {
		return fi.Size()
	}
	return 0
}
