package llamacpp

import (
	"os"
	"path/filepath"
	"testing"
)

func writeLib(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, LibName()), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestLocateOrderAndConfigKey(t *testing.T) {
	root := t.TempDir()
	env, cfgDir, xdg := filepath.Join(root, "env"), filepath.Join(root, "cfg"), filepath.Join(root, "xdg")
	cfgFile := filepath.Join(root, "config.json")
	if err := os.WriteFile(cfgFile, []byte(`{"llama_lib_dir":"`+cfgDir+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := Locate("", cfgFile, xdg); ok {
		if _, err := os.Stat("/opt/llama.cpp/build/bin/" + LibName()); err != nil {
			t.Fatal("found a library where none exists")
		}
	}
	writeLib(t, xdg)
	if d, ok := Locate("", cfgFile, xdg); !ok || d != xdg {
		if _, err := os.Stat("/opt/llama.cpp/build/bin/" + LibName()); err != nil {
			t.Fatalf("xdg fallback: %q %v", d, ok)
		}
	}
	writeLib(t, cfgDir)
	if d, ok := Locate("", cfgFile, xdg); !ok || d != cfgDir {
		t.Fatalf("the llama_lib_dir config key must beat the data dir: %q %v", d, ok)
	}
	writeLib(t, env)
	if d, ok := Locate(env, cfgFile, xdg); !ok || d != env {
		t.Fatalf("the environment must win: %q %v", d, ok)
	}
}
