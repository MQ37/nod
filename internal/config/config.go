// Package config holds the nod config (a small JSON file in the XDG config
// dir), the XDG path helpers and key based get/set with validation. Stdlib only.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Paths are the directories nod uses. All derive from XDG variables.
type Paths struct {
	ConfigFile string // config.json
	ConfigDir  string // XDG_CONFIG_HOME
	StateDir   string // state.json lives here
	DataDir    string // models and libs
	RuntimeDir string // decisions.jsonl (RAM)
	Home       string
}

// ResolvePaths computes the paths. override replaces the config file path (--config).
func ResolvePaths(getenv func(string) string, home string, uid int, override string) Paths {
	xdg := func(name, fallback string) string {
		if v := getenv(name); v != "" && filepath.IsAbs(v) {
			return v
		}
		return filepath.Join(home, fallback)
	}
	p := Paths{Home: home}
	p.ConfigDir = xdg("XDG_CONFIG_HOME", ".config")
	p.ConfigFile = filepath.Join(p.ConfigDir, "nod", "config.json")
	if override != "" {
		p.ConfigFile = override
	}
	p.StateDir = filepath.Join(xdg("XDG_STATE_HOME", ".local/state"), "nod")
	p.DataDir = filepath.Join(xdg("XDG_DATA_HOME", ".local/share"), "nod")
	if r := getenv("XDG_RUNTIME_DIR"); r != "" && filepath.IsAbs(r) {
		p.RuntimeDir = filepath.Join(r, "nod")
	} else {
		// Keep the hook writer and `nod decisions` on the same volatile path.
		if uid < 0 {
			uid = os.Getuid()
		}
		runtimeBase := os.TempDir()
		if st, err := os.Stat("/dev/shm"); err == nil && st.IsDir() {
			runtimeBase = "/dev/shm"
		}
		p.RuntimeDir = filepath.Join(runtimeBase, "nod-"+strconv.Itoa(uid))
	}
	return p
}

// StateFile is the install record path.
func (p Paths) StateFile() string { return filepath.Join(p.StateDir, "state.json") }

// DecisionsFile is the verdict log path.
func (p Paths) DecisionsFile() string { return filepath.Join(p.RuntimeDir, "decisions.jsonl") }

// ModelsDir is where model GGUFs are stored.
func (p Paths) ModelsDir() string { return filepath.Join(p.DataDir, "models") }

// LibDir is where the pinned llama.cpp libraries are stored.
func (p Paths) LibDir() string { return filepath.Join(p.DataDir, "lib") }

// Config is the persisted configuration. JSON names are the keys of `config get|set`.
type Config struct {
	Addr           string `json:"addr"`             // local daemon listen address
	Remote         string `json:"remote"`           // remote server URL; empty = use the local daemon
	Model          string `json:"model"`            // tier: full | lite | fast
	ModelPath      string `json:"model_path"`       // explicit GGUF path (overrides the tier)
	LlamaLibDir    string `json:"llama_lib_dir"`    // directory holding your own llama.cpp libraries (e.g. a GPU build)
	ServiceName    string `json:"service_name"`     // user service identity
	IdleUnloadSecs int    `json:"idle_unload_secs"` // unload the model after this many idle seconds (0 = never)
	WarmWaitSecs   int    `json:"warm_wait_secs"`   // maximum time a cold request waits for model loading
	OnUnreachable  string `json:"on_unreachable"`   // ask | block | allow
	Mode           string `json:"mode"`             // shadow | enforce
	ShadowUntil    string `json:"shadow_until"`     // RFC3339; shadow ends then
	TimeoutSecs    int    `json:"timeout_secs"`     // client timeout for check/hooks
	RemoteFallback string `json:"remote_fallback"`  // with a remote set: local = fall back to the local daemon when the remote is down; none = ask
	LocalPower     string `json:"local_power"`      // when the local model may run: plugged | always | never
	Engine         string `json:"engine"`           // hybrid (tiers + rules + model) | model (the model judges every call)
	LogCommands    bool   `json:"log_commands"`     // also log command text in the verdict log
}

// Default returns the default configuration.
func Default() Config {
	return Config{
		Addr: "127.0.0.1:11096", OnUnreachable: "ask", Engine: "model", RemoteFallback: "local", LocalPower: "plugged", Mode: "shadow", TimeoutSecs: 10, IdleUnloadSecs: 900, WarmWaitSecs: 5, ServiceName: "nod",
	}
}

// ServerURL is the base URL clients use: the remote if set, else the local daemon.
func (c Config) ServerURL() string {
	if c.Remote != "" {
		return strings.TrimRight(c.Remote, "/")
	}
	return "http://" + c.Addr
}

// LocalURL is the local daemon, regardless of any remote.
func (c Config) LocalURL() string { return "http://" + c.Addr }

type key struct {
	desc string
	get  func(*Config) string
	set  func(*Config, string) error
}

func enum(name string, vals ...string) func(string) error {
	return func(v string) error {
		for _, x := range vals {
			if x == v {
				return nil
			}
		}
		return fmt.Errorf("%s must be one of %s", name, strings.Join(vals, "|"))
	}
}

func intKey(name string, ptr func(*Config) *int, min int) key {
	return key{
		get: func(c *Config) string { return strconv.Itoa(*ptr(c)) },
		set: func(c *Config, v string) error {
			n, err := strconv.Atoi(v)
			if err != nil || n < min {
				return fmt.Errorf("%s must be an integer >= %d", name, min)
			}
			*ptr(c) = n
			return nil
		},
	}
}

func strKey(ptr func(*Config) *string, check func(string) error) key {
	return key{
		get: func(c *Config) string { return *ptr(c) },
		set: func(c *Config, v string) error {
			if check != nil {
				if err := check(v); err != nil {
					return err
				}
			}
			*ptr(c) = v
			return nil
		},
	}
}

// ValidateURL accepts empty or an absolute http(s) URL without credentials.
func ValidateURL(v string) error {
	if v == "" {
		return nil
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("must be an http(s) URL such as https://guard.example:11096")
	}
	if u.User != nil {
		return errors.New("must not contain credentials")
	}
	return nil
}

func validAddr(v string) error {
	host, port, err := net.SplitHostPort(v)
	if err != nil || host == "" || port == "" {
		return errors.New("addr must be host:port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return errors.New("addr port must be 1-65535")
	}
	return nil
}

var keys = map[string]key{
	"addr":             strKey(func(c *Config) *string { return &c.Addr }, validAddr),
	"remote":           strKey(func(c *Config) *string { return &c.Remote }, ValidateURL),
	"model":            strKey(func(c *Config) *string { return &c.Model }, func(v string) error { return optEnum(v, "full", "lite", "fast", "agent") }),
	"model_path":       strKey(func(c *Config) *string { return &c.ModelPath }, nil),
	"llama_lib_dir":    strKey(func(c *Config) *string { return &c.LlamaLibDir }, nil),
	"service_name":     strKey(func(c *Config) *string { return &c.ServiceName }, ValidateServiceName),
	"idle_unload_secs": intKey("idle_unload_secs", func(c *Config) *int { return &c.IdleUnloadSecs }, 0),
	"warm_wait_secs":   intKey("warm_wait_secs", func(c *Config) *int { return &c.WarmWaitSecs }, 0),
	"on_unreachable":   strKey(func(c *Config) *string { return &c.OnUnreachable }, enum("on_unreachable", "ask", "block", "allow")),
	"remote_fallback":  strKey(func(c *Config) *string { return &c.RemoteFallback }, enum("remote_fallback", "local", "none")),
	"local_power":      strKey(func(c *Config) *string { return &c.LocalPower }, enum("local_power", "plugged", "always", "never")),
	"engine":           strKey(func(c *Config) *string { return &c.Engine }, enum("engine", "hybrid", "model")),
	"mode":             strKey(func(c *Config) *string { return &c.Mode }, enum("mode", "shadow", "enforce")),
	"shadow_until":     strKey(func(c *Config) *string { return &c.ShadowUntil }, nil),
	"timeout_secs":     intKey("timeout_secs", func(c *Config) *int { return &c.TimeoutSecs }, 1),
	"log_commands": {
		get: func(c *Config) string { return strconv.FormatBool(c.LogCommands) },
		set: func(c *Config, v string) error {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return errors.New("log_commands must be true or false")
			}
			c.LogCommands = b
			return nil
		},
	},
}

// ValidateServiceName accepts names safe to use as systemd units and launchd labels.
func ValidateServiceName(v string) error {
	if v == "" {
		return errors.New("service_name must match [a-z0-9-]+")
	}
	for _, r := range v {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return errors.New("service_name must match [a-z0-9-]+")
	}
	return nil
}

func optEnum(v string, vals ...string) error {
	if v == "" {
		return nil
	}
	return enum("model", vals...)(v)
}

// Keys lists the known keys, sorted.
func Keys() []string {
	var k []string
	for n := range keys {
		k = append(k, n)
	}
	sort.Strings(k)
	return k
}

// Get returns the value of a key as text.
func (c Config) Get(name string) (string, error) {
	k, ok := keys[name]
	if !ok {
		return "", unknown(name)
	}
	return k.get(&c), nil
}

// Set validates and sets a key.
func (c *Config) Set(name, value string) error {
	k, ok := keys[name]
	if !ok {
		return unknown(name)
	}
	return k.set(c, value)
}

func unknown(name string) error {
	return fmt.Errorf("unknown config key %q (keys: %s)", name, strings.Join(Keys(), ", "))
}

// Validate checks every field.
func (c Config) Validate() error {
	for _, n := range Keys() {
		v, _ := c.Get(n)
		cc := Default()
		if err := cc.Set(n, v); err != nil {
			// empty optional strings are fine for these keys
			if v == "" && (n == "model_path" || n == "shadow_until" || n == "remote" || n == "model") {
				continue
			}
			return fmt.Errorf("%s: %w", n, err)
		}
	}
	return nil
}

// Load reads the config file; a missing file gives the defaults. Unknown keys are ignored.
func Load(path string) (Config, error) {
	c := Default()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return Default(), fmt.Errorf("%s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Marshal renders the canonical file content.
func (c Config) Marshal() []byte {
	b, _ := json.MarshalIndent(c, "", "  ")
	return append(b, '\n')
}

// Save writes the config atomically with mode 0600.
func Save(path string, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if old, err := os.ReadFile(path); err == nil {
		if bytes.Equal(old, c.Marshal()) {
			return nil
		}
		stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
		backup := path + ".nod-" + stamp + ".bak"
		if err := os.WriteFile(backup, old, 0o600); err != nil {
			return fmt.Errorf("create config backup %s: %w", backup, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return WriteFileAtomic(path, c.Marshal(), 0o600)
}

// WriteFileAtomic writes data to path via a temp file + rename, creating parent dirs (0700).
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
