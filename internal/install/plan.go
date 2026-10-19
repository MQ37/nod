// Package install describes a reviewed installation before any files are changed.
package install

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/MQ37/nod/internal/models"
)

// Step lists the effects of one operation. Diffs are populated for text edits.
type Step struct {
	Description string            `json:"description"`
	Files       []string          `json:"files,omitempty"`
	Diffs       map[string]string `json:"diffs,omitempty"`
}

// Plan is the complete set of planned actions.
type Plan struct {
	OS       string   `json:"os"`
	Arch     string   `json:"arch"`
	RAMBytes uint64   `json:"ram_bytes"`
	GPU      bool     `json:"gpu"`
	Tier     string   `json:"tier"`
	Mode     string   `json:"mode"`
	Hosts    []string `json:"hosts"`
	Steps    []Step   `json:"steps"`
	Rollback string   `json:"rollback"`
}

// Options configures plan generation.
type Options struct {
	Home, ConfigPath, Addr, Remote, Mode, OnUnreachable, Tier string
	// ModelPath and LibDir are the user's own GGUF and llama.cpp libraries. Nod downloads neither.
	ModelPath, LibDir string
	Hosts             []string
	ServerOnly        bool
}

// DetectRAM returns RAM bytes using OS-provided sources only.
func DetectRAM(goos string, readFile func(string) ([]byte, error)) uint64 {
	readInjected := readFile != nil
	if readFile == nil {
		readFile = os.ReadFile
	}
	if goos == "linux" {
		if b, e := readFile("/proc/meminfo"); e == nil {
			for _, line := range strings.Split(string(b), "\n") {
				if strings.HasPrefix(line, "MemTotal:") {
					f := strings.Fields(line)
					if len(f) >= 2 {
						n, _ := strconv.ParseUint(f[1], 10, 64)
						return n * 1024
					}
				}
			}
		}
	}
	if goos == "darwin" {
		var b []byte
		var err error
		if !readInjected {
			b, err = exec.Command("/usr/sbin/sysctl", "-n", "hw.memsize").Output()
		} else {
			b, err = readFile("sysctl:hw.memsize")
		}
		if err == nil {
			n, _ := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
			return n
		}
	}
	return 0
}

// DetectGPU uses device file presence on Linux and Metal's platform presence on macOS.
func DetectGPU(goos string, stat func(string) (os.FileInfo, error)) bool {
	if stat == nil {
		stat = os.Stat
	}
	if goos == "darwin" {
		return true
	}
	if goos != "linux" {
		return false
	}
	if entries, e := os.ReadDir("/sys/class/drm"); e == nil {
		for _, x := range entries {
			if strings.HasPrefix(x.Name(), "card") && !strings.Contains(x.Name(), "-") {
				return true
			}
		}
	}
	for _, p := range []string{"/dev/dri", "/dev/kfd"} {
		if _, e := stat(p); e == nil {
			return true
		}
	}
	for _, name := range []string{"rocminfo", "vulkaninfo"} {
		if commandExists(name, stat) {
			return true
		}
	}
	return false
}

func commandExists(name string, stat func(string) (os.FileInfo, error)) bool {
	if stat == nil {
		stat = os.Stat
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if fi, err := stat(filepath.Join(dir, name)); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return true
		}
	}
	return false
}

// RecommendTier uses the published memory thresholds. GPUs are detected, never queried by running a binary.
func RecommendTier(ram uint64, gpu bool) string { return models.Recommend(ram, gpu) }

// BuildPlan produces the same inspectable plan for interactive, dry-run and execute paths.
func BuildPlan(o Options, goos, arch string, ram uint64, gpu bool) (Plan, error) {
	if o.Mode == "" {
		o.Mode = "shadow"
	}
	if o.OnUnreachable == "" {
		o.OnUnreachable = "ask"
	}
	if o.Mode != "shadow" && o.Mode != "enforce" {
		return Plan{}, fmt.Errorf("mode must be shadow or enforce")
	}
	if o.Tier == "" {
		o.Tier = RecommendTier(ram, gpu)
	}
	if o.Tier != "full" && o.Tier != "lite" && o.Tier != "fast" && o.Tier != "agent" {
		return Plan{}, fmt.Errorf("tier must be full, lite, fast or agent")
	}
	if o.ConfigPath == "" {
		o.ConfigPath = filepath.Join(o.Home, ".config", "nod", "config.json")
	}
	if o.Addr == "" {
		o.Addr = "127.0.0.1:11096"
	}
	content := fmt.Sprintf("{\n  \"addr\": %q,\n  \"mode\": %q,\n  \"remote\": %q,\n  \"on_unreachable\": %q,\n  \"model\": %q,\n  \"model_path\": %q,\n  \"llama_lib_dir\": %q\n}\n", o.Addr, o.Mode, o.Remote, o.OnUnreachable, o.Tier, o.ModelPath, o.LibDir)
	p := Plan{OS: goos, Arch: arch, RAMBytes: ram, GPU: gpu, Tier: o.Tier, Mode: o.Mode, Hosts: append([]string(nil), o.Hosts...), Rollback: "nod uninstall --yes"}
	s := Step{Description: "write nod configuration", Files: []string{o.ConfigPath}, Diffs: map[string]string{o.ConfigPath: "--- a/" + o.ConfigPath + "\n+++ b/" + o.ConfigPath + "\n@@\n+" + strings.ReplaceAll(content, "\n", "\n+")}}
	p.Steps = append(p.Steps, s)
	if o.Tier == "agent" {
		p.Steps = append(p.Steps, Step{Description: "no local model: the model your agent is already running reviews each call (one extra request per gated call)"})
	} else {
		p.Steps = append(p.Steps, Step{Description: "use the model and llama.cpp libraries you provided (nothing is downloaded)", Files: []string{o.ModelPath, o.LibDir}})
	}
	p.Steps = append(p.Steps, Step{Description: "install and start the per-user nod service"})
	return p, nil
}

// RenderJSON returns stable indented JSON for --dry-run --json.
func (p Plan) RenderJSON() ([]byte, error) { return json.MarshalIndent(p, "", "  ") }
