package hosts

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SHA is the hex sha256 of b ("" for nil, meaning absent).
func SHA(b []byte) string {
	if b == nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// ReadFile returns the content of path, or nil, nil when it does not exist.
func ReadFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if b == nil {
		b = []byte{}
	}
	return b, nil
}

// BackupName is path + ".bak-nod-YYYYmmdd-HHMMSS".
func BackupName(path string, now time.Time) string {
	return path + ".bak-nod-" + now.Format("20060102-150405")
}

// ResolveLink follows a symlinked config file (dotfile setups) so the link itself is kept.
func ResolveLink(path string) string {
	if p, err := filepath.EvalSymlinks(path); err == nil {
		return p
	}
	return path
}

// WriteAtomic writes b to path through a temp file in the same directory and a rename.
func WriteAtomic(path string, b []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".nod-tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// Apply executes the steps of a plan. A step whose file changed since planning is refused.
func Apply(env Env, plan Plan) error {
	env = env.Norm()
	for _, s := range plan.Steps {
		if s.Op == "none" || s.Op == "" {
			continue
		}
		if s.Op == "rmdir" {
			_ = os.Remove(s.Path)
			continue
		}
		cur, err := ReadFile(s.Path)
		if err != nil {
			return err
		}
		if SHA(cur) != s.BaseSHA {
			return fmt.Errorf("%s changed since the plan was made; run again", s.Path)
		}
		if s.Backup && cur != nil {
			bk := BackupName(s.Path, env.Now())
			for i := 1; ; i++ {
				if _, err := os.Lstat(bk); os.IsNotExist(err) {
					break
				}
				bk = fmt.Sprintf("%s-%d", BackupName(s.Path, env.Now()), i)
			}
			mode := s.Mode
			if st, err := os.Stat(s.Path); err == nil {
				mode = st.Mode().Perm()
			}
			if err := WriteAtomic(bk, cur, mode); err != nil {
				return fmt.Errorf("backup of %s failed, nothing changed: %w", s.Path, err)
			}
		}
		switch s.Op {
		case "rmdir": // only when empty
			_ = os.Remove(s.Path)
		case "delete":
			if err := os.Remove(s.Path); err != nil && !os.IsNotExist(err) {
				return err
			}
		case "create", "modify":
			mode := s.Mode
			if mode == 0 {
				mode = 0o644
			}
			if st, err := os.Stat(s.Path); err == nil {
				mode = st.Mode().Perm()
			}
			if err := WriteAtomic(s.Path, s.Content, mode); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown step op %q", s.Op)
		}
	}
	return nil
}

// FileStep plans writing want to path: create, modify (with backup when backup is set) or none.
func FileStep(path string, want []byte, mode os.FileMode, backup bool, desc string) (Step, error) {
	cur, err := ReadFile(path)
	if err != nil {
		return Step{}, err
	}
	st := Step{Path: path, Op: "none", Desc: desc, Mode: mode, BaseSHA: SHA(cur)}
	if cur != nil && string(cur) == string(want) {
		return st, nil
	}
	st.Content = want
	if cur == nil {
		st.Op = "create"
	} else {
		st.Op, st.Backup = "modify", backup
	}
	st.Diff = UnifiedDiff(path, string(cur), string(want))
	return st, nil
}

// DeleteStep plans deleting path (none when absent).
func DeleteStep(path, desc string) (Step, error) {
	cur, err := ReadFile(path)
	if err != nil {
		return Step{}, err
	}
	if cur == nil {
		return Step{Path: path, Op: "none", Desc: desc}, nil
	}
	return Step{Path: path, Op: "delete", Desc: desc, BaseSHA: SHA(cur), Diff: UnifiedDiff(path, string(cur), "")}, nil
}
