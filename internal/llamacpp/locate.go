package llamacpp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
)

// LibName is the shared library the binding dlopens.
func LibName() string {
	if runtime.GOOS == "windows" {
		return "llama.dll"
	}
	if runtime.GOOS == "darwin" {
		return "libllama.dylib"
	}
	return "libllama.so"
}

// HasLib reports whether dir contains the llama.cpp shared library.
func HasLib(dir string) bool {
	if dir == "" {
		return false
	}
	st, err := os.Stat(filepath.Join(dir, LibName()))
	return err == nil && st.Mode().IsRegular()
}

// Locate searches env, the config file's llama_lib_dir, /opt/llama.cpp/build/bin,
// then xdgDir. The first directory that contains the library wins.
func Locate(envDir, configFile, xdgDir string) (string, bool) {
	var dirs []string
	if envDir != "" {
		dirs = append(dirs, envDir)
	}
	if d := configLibDir(configFile); d != "" {
		dirs = append(dirs, d)
	}
	dirs = append(dirs, "/opt/llama.cpp/build/bin", xdgDir)
	for _, d := range dirs {
		if HasLib(d) {
			return d, true
		}
	}
	return "", false
}

func configLibDir(path string) string {
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var cfg struct {
		LlamaLibDir string `json:"llama_lib_dir"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return ""
	}
	return cfg.LlamaLibDir
}
