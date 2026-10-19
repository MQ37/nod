package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateAddress(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{"127.0.0.1:11096", true}, {"[::1]:11096", true}, {"localhost:1", true}, {"localhost:65536", false}, {"localhost", false}, {"bad:port", false}, {":11096", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			err := validAddr(tc.value)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}

func TestIdleDefaults(t *testing.T) {
	c := Default()
	if c.IdleUnloadSecs != 900 || c.WarmWaitSecs != 5 {
		t.Fatalf("idle=%d warm=%d", c.IdleUnloadSecs, c.WarmWaitSecs)
	}
	if err := c.Set("idle_unload_secs", "0"); err != nil || c.IdleUnloadSecs != 0 {
		t.Fatalf("zero idle should keep model resident: %v", err)
	}
	if c.ServiceName != "nod" {
		t.Fatalf("service name default=%q", c.ServiceName)
	}
}

func TestServiceNameValidationAndXDGPaths(t *testing.T) {
	for _, tc := range []struct {
		name string
		ok   bool
	}{
		{"nod", true}, {"nod-sandbox2", true}, {"", false}, {"Nod", false}, {"tool.guard", false}, {"../other", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			err := c.Set("service_name", tc.name)
			if (err == nil) != tc.ok {
				t.Fatalf("valid=%v err=%v", tc.ok, err)
			}
		})
	}
	getenv := func(k string) string {
		switch k {
		case "XDG_CONFIG_HOME":
			return "/scratch/config"
		case "XDG_DATA_HOME":
			return "/scratch/data"
		case "XDG_STATE_HOME":
			return "/scratch/state"
		default:
			return ""
		}
	}
	p := ResolvePaths(getenv, "/home/test", 99, "")
	if p.ConfigDir != "/scratch/config" || p.ConfigFile != "/scratch/config/nod/config.json" || p.DataDir != "/scratch/data/nod" || p.StateDir != "/scratch/state/nod" {
		t.Fatalf("paths=%+v", p)
	}
}

func TestSaveCreatesTimestampedBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	cfg.Mode = "enforce"
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(path + ".nod-*.bak")
	if err != nil || len(matches) != 1 {
		t.Fatalf("backups=%v err=%v", matches, err)
	}
	b, err := os.ReadFile(matches[0])
	if err != nil || string(b) != "old\n" {
		t.Fatalf("backup=%q err=%v", b, err)
	}
	if !strings.Contains(filepath.Base(matches[0]), ".nod-") {
		t.Fatalf("bad backup name: %s", matches[0])
	}
}
