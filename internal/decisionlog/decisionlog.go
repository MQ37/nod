// Package decisionlog is nod's audit trail: an append-only JSONL log of every verdict, one file per
// host, kept in a persistent directory ($XDG_STATE_HOME/nod/audit by default). Every line carries the
// sha256 of the previous line, so deleted, edited or reordered lines are detectable with Verify (this
// is tamper-evidence, not a security boundary: the same user can rewrite a whole file). Files are 0600
// in a 0700 directory. The command is stored redacted (see CommandFields).
package decisionlog

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// FormatVersion is the line format version (field "v").
const FormatVersion = 1

// DefaultMaxBytes is the size at which a log rotates; DefaultKeep rotated files are kept.
const (
	DefaultMaxBytes = 10 << 20
	DefaultKeep     = 5
)

// Genesis is the "prev" of the first line ever written to a host's log.
var Genesis = strings.Repeat("0", 64)

// Entry is one audit line.
type Entry struct {
	V             int       `json:"v,omitempty"`
	Time          time.Time `json:"time"`
	Host          string    `json:"host,omitempty"` // which adapter asked: check, claude, hermes, opencode, omp...
	Session       string    `json:"session,omitempty"`
	Mode          string    `json:"mode,omitempty"`
	Review        string    `json:"review,omitempty"` // decider | agent
	Tool          string    `json:"tool"`
	Command       string    `json:"command,omitempty"`        // redacted, clipped
	CommandSHA256 string    `json:"command_sha256,omitempty"` // of the original command
	Decision      string    `json:"decision"`
	Source        string    `json:"source,omitempty"`
	Model         string    `json:"model,omitempty"` // the reviewing model in agent review
	Rules         []string  `json:"rules,omitempty"`
	Answer        string    `json:"answer,omitempty"`
	Category      string    `json:"category,omitempty"`
	Ms            int       `json:"ms,omitempty"`
	Error         string    `json:"error,omitempty"`
	Cwd           string    `json:"cwd,omitempty"`
	Reason        string    `json:"reason,omitempty"`
	Prev          string    `json:"prev,omitempty"` // sha256 of the previous line, set by Append
}

// Writer appends entries. Safe across goroutines and processes (an flock serialises appenders).
type Writer struct {
	Path     string
	MaxBytes int64
	Keep     int
	Verbose  bool // retained for API compatibility
	mu       sync.Mutex
}

// New returns a writer for path.
func New(path string, verbose bool) *Writer {
	return &Writer{Path: path, MaxBytes: DefaultMaxBytes, Keep: DefaultKeep, Verbose: verbose}
}

func lineHash(line []byte) string {
	sum := sha256.Sum256(line)
	return hex.EncodeToString(sum[:])
}

// lastLine returns the last non-empty line of a file ("" when none), reading only the tail.
func lastLine(f *os.File) ([]byte, error) {
	fi, err := f.Stat()
	if err != nil || fi.Size() == 0 {
		return nil, err
	}
	const tail = 1 << 20
	start := int64(0)
	if fi.Size() > tail {
		start = fi.Size() - tail
	}
	buf := make([]byte, fi.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	buf = bytes.TrimRight(buf, "\n")
	if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
		buf = buf[i+1:]
	}
	return buf, nil
}

// Append writes one entry, chaining it to the previous line. Errors are returned; callers on the hot
// path may ignore them.
func (w *Writer) Append(e Entry) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	e.V = FormatVersion
	if err := os.MkdirAll(filepath.Dir(w.Path), 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(w.Path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	f, err := os.OpenFile(w.Path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	prev := Genesis
	last, err := lastLine(f)
	if err != nil {
		f.Close()
		return err
	}
	if len(last) == 0 {
		// A new file: continue the chain of a rotated predecessor, if any.
		if g, err := os.Open(w.Path + ".1"); err == nil {
			if l, err := lastLine(g); err == nil && len(l) > 0 {
				prev = lineHash(l)
			}
			g.Close()
		}
	} else {
		prev = lineHash(last)
	}
	e.Prev = prev
	line, err := json.Marshal(e)
	if err != nil {
		f.Close()
		return err
	}
	if w.MaxBytes > 0 {
		if fi, err := f.Stat(); err == nil && fi.Size() > 0 && fi.Size()+int64(len(line))+1 > w.MaxBytes {
			f.Close()
			w.rotate()
			f, err = os.OpenFile(w.Path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
			if err != nil {
				return err
			}
			if err := f.Chmod(0o600); err != nil {
				f.Close()
				return err
			}
			// prev stays the hash of the last line of the file we just rotated away.
		}
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (w *Writer) rotate() {
	keep := w.Keep
	if keep <= 0 {
		keep = DefaultKeep
	}
	_ = os.Remove(w.Path + "." + strconv.Itoa(keep))
	for i := keep - 1; i >= 1; i-- {
		_ = os.Rename(w.Path+"."+strconv.Itoa(i), w.Path+"."+strconv.Itoa(i+1))
	}
	_ = os.Rename(w.Path, w.Path+".1")
}

// files lists the existing log files of path, oldest first.
func files(path string, keep int) []string {
	if keep <= 0 {
		keep = DefaultKeep
	}
	var out []string
	for i := keep; i >= 1; i-- {
		p := path + "." + strconv.Itoa(i)
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	if _, err := os.Stat(path); err == nil {
		out = append(out, path)
	}
	return out
}

// ReadLast returns up to n most recent entries (oldest first), reading rotated files when needed.
// Unparseable lines are skipped. A missing log is empty.
func ReadLast(path string, n int) ([]Entry, error) {
	var all []Entry
	for _, p := range files(path, DefaultKeep) {
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
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// Break describes the first place a chain does not hold.
type Break struct {
	File   string
	Line   int
	Reason string
}

func (b *Break) Error() string { return fmt.Sprintf("%s line %d: %s", b.File, b.Line, b.Reason) }

// VerifyResult is the outcome of Verify.
type VerifyResult struct {
	Lines int
	Files []string
	Break *Break // nil when the chain holds
	// Truncated is true when the oldest line's prev is not the genesis value: older files were rotated
	// away, which is normal retention, not tampering.
	Truncated bool
}

// Verify walks a host's log files oldest to newest and checks the hash chain.
func Verify(path string) (VerifyResult, error) {
	res := VerifyResult{Files: files(path, DefaultKeep)}
	var prevHash string
	first := true
	for _, p := range res.Files {
		f, err := os.Open(p)
		if err != nil {
			return res, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), 4<<20)
		n := 0
		for sc.Scan() {
			n++
			raw := append([]byte(nil), sc.Bytes()...)
			if len(bytes.TrimSpace(raw)) == 0 {
				continue
			}
			var e Entry
			if err := json.Unmarshal(raw, &e); err != nil {
				f.Close()
				res.Break = &Break{File: p, Line: n, Reason: "line is not valid JSON"}
				return res, nil
			}
			if first {
				first = false
				res.Truncated = e.Prev != Genesis
			} else if e.Prev != prevHash {
				f.Close()
				res.Break = &Break{File: p, Line: n, Reason: "prev does not match the hash of the previous line (a line was changed, removed or inserted)"}
				return res, nil
			}
			prevHash = lineHash(raw)
			res.Lines++
		}
		err = sc.Err()
		f.Close()
		if err != nil {
			return res, err
		}
	}
	return res, nil
}

// Size returns the current size of the log file (0 when missing).
func Size(path string) int64 {
	if fi, err := os.Stat(path); err == nil {
		return fi.Size()
	}
	return 0
}
