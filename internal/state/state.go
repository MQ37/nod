// Package state records exactly what `nod install` created or edited, so
// `uninstall` can reverse it. The file is state.json in the XDG state dir.
package state

import (
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/MQ37/nod/internal/config"
)

// Record kinds.
const (
	KindFile    = "file"    // a file we wrote (Created: remove; Backup: restore)
	KindModel   = "model"   // a downloaded model (SHA256 recorded)
	KindLib     = "lib"     // a downloaded llama.cpp lib dir or file
	KindService = "service" // the installed user service
	KindHooks   = "hooks"   // a host wired by `hooks install` (Host)
	KindDir     = "dir"     // a directory we created (removed when empty)
)

// Record is one reversible action.
type Record struct {
	Kind    string `json:"kind"`
	Path    string `json:"path,omitempty"`
	Host    string `json:"host,omitempty"`
	Created bool   `json:"created,omitempty"` // path did not exist before
	Backup  string `json:"backup,omitempty"`  // copy of the previous content
	SHA256  string `json:"sha256,omitempty"`
	Size    int64  `json:"size,omitempty"`
}

// State is the install record.
type State struct {
	Version     int       `json:"version"`
	InstalledAt time.Time `json:"installed_at"`
	Binary      string    `json:"binary"`
	BinaryVer   string    `json:"binary_version"`
	Tier        string    `json:"tier,omitempty"`
	ServiceName string    `json:"service_name,omitempty"`
	Remote      string    `json:"remote,omitempty"`
	Hosts       []string  `json:"hosts,omitempty"`
	Records     []Record  `json:"records"`
}

// ErrNotInstalled means there is no state file.
var ErrNotInstalled = errors.New("nod is not installed (no state.json)")

// Load reads the state file.
func Load(path string) (*State, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotInstalled
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Save writes the state atomically (0600).
func (s *State) Save(path string) error {
	s.Version = 1
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(path, append(b, '\n'), 0o600)
}

// Add records an action, replacing an earlier record of the same kind/path/host
// (re-running install must not duplicate entries).
func (s *State) Add(r Record) {
	for i, e := range s.Records {
		if e.Kind == r.Kind && e.Path == r.Path && e.Host == r.Host {
			// keep the original "Created"/"Backup": they describe the state before nod
			if e.Created {
				r.Created = true
			}
			if e.Backup != "" {
				r.Backup = e.Backup
			}
			s.Records[i] = r
			return
		}
	}
	s.Records = append(s.Records, r)
}

// Find returns the first record of kind with path (or host when path is empty).
func (s *State) Find(kind, path string) (Record, bool) {
	for _, r := range s.Records {
		if r.Kind == kind && (r.Path == path || (path == "")) {
			return r, true
		}
	}
	return Record{}, false
}

// Remove deletes the matching record.
func (s *State) Remove(r Record) {
	out := s.Records[:0]
	for _, e := range s.Records {
		if !(e.Kind == r.Kind && e.Path == r.Path && e.Host == r.Host) {
			out = append(out, e)
		}
	}
	s.Records = out
}
