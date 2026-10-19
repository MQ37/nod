package models

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Result is the outcome for one expected file in a directory.
type Result struct {
	Path   string `json:"path"`
	Status string `json:"status"` // ok | missing | size | checksum | unpinned
	Detail string `json:"detail,omitempty"`
}

// sidecars are the small files that must sit next to the GGUF.
var sidecars = []string{"tokenizer.json", "decider_config.json"}

// ChatSidecarName is the file that selects the chat prompt layout (decider-12b).
const ChatSidecarName = "decider_chat.json"

// chatSidecar is the Gemma-4 chat template pieces and the token ids they must produce.
const chatSidecar = `{"layout":"chat","head":"<bos><|turn>user\n","tail":"<turn|>\n<|turn>model\n<|channel>thought\n<channel|>","answer":"Answer","head_ids":[2,105,2364,107],"tail_ids":[106,107,105,4368,107,100,45518,107,101]}`

// WriteChatSidecar writes decider_chat.json into dir when the tier needs one.
func WriteChatSidecar(t Tier, dir string) (string, error) {
	if t.ID != "lite" {
		return "", fmt.Errorf("%s does not use the chat layout; only lite needs %s", t.ID, ChatSidecarName)
	}
	p := filepath.Join(dir, ChatSidecarName)
	return p, os.WriteFile(p, []byte(chatSidecar), 0o644)
}

// VerifyDir checks a directory the user (or their agent) prepared: the GGUF and the small files
// next to it. Pinned files must match their sha256. A converted GGUF has no published hash, so it
// is reported as unpinned with its computed sha256 instead of failing.
func VerifyDir(t Tier, dir string) []Result {
	var files []File
	if t.Source == "hf-gguf" {
		files = t.Files
	} else {
		files = []File{t.GGUFFile()}
		src, _ := t.SourceFiles()
		for _, name := range sidecars {
			f := File{Path: name, SHA256: unpinned}
			for _, s := range src {
				if s.Path == name {
					f = s
				}
			}
			files = append(files, f)
		}
	}
	var out []Result
	for _, f := range files {
		out = append(out, checkOne(dir, f))
	}
	if t.ID == "lite" {
		r := Result{Path: ChatSidecarName, Status: "ok"}
		if _, err := os.Stat(filepath.Join(dir, ChatSidecarName)); err != nil {
			r = Result{Path: ChatSidecarName, Status: "missing", Detail: "run: nod models sidecar lite " + dir}
		}
		out = append(out, r)
	}
	return out
}

func checkOne(dir string, f File) Result {
	p := filepath.Join(dir, filepath.FromSlash(f.Path))
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() {
		return Result{Path: f.Path, Status: "missing"}
	}
	if f.Size > 0 && st.Size() != f.Size {
		return Result{Path: f.Path, Status: "size", Detail: fmt.Sprintf("is %d bytes, published %d (truncated download?)", st.Size(), f.Size)}
	}
	sum, err := HashFile(p)
	if err != nil {
		return Result{Path: f.Path, Status: "missing", Detail: err.Error()}
	}
	if !f.Pinned() {
		return Result{Path: f.Path, Status: "unpinned", Detail: "no published sha256 to compare; computed " + sum}
	}
	if !strings.EqualFold(sum, f.SHA256) {
		return Result{Path: f.Path, Status: "checksum", Detail: "sha256 " + sum + " does not match the published " + f.SHA256}
	}
	return Result{Path: f.Path, Status: "ok"}
}

// Failed reports whether any result is a hard failure (unpinned is not).
func Failed(rs []Result) bool {
	for _, r := range rs {
		if r.Status != "ok" && r.Status != "unpinned" {
			return true
		}
	}
	return false
}
