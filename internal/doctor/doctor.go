// Package doctor reports installation and daemon health without changing files.
package doctor

import (
	"os"
	"strconv"
	"time"
)

// Check is a machine-readable diagnostic.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// Input contains independently injectable probes.
type Input struct {
	Binary           string
	ConfigOK         bool
	ConfigDetail     string
	ServiceInstalled bool
	ServiceActive    bool
	ServerHealthy    bool
	ServerDetail     string
	HooksOK          bool
	HookDetail       string
	ModelsOK         bool
	ModelsDetail     string
	LibsOK           bool
	LibsDetail       string
	Latency          time.Duration
	ModelStatus      string
	IdleSeconds      int
}

// Run returns stable named checks in user-facing order.
func Run(in Input) []Check {
	binOK := false
	detail := "binary not found"
	if in.Binary != "" {
		if fi, err := os.Stat(in.Binary); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			binOK = true
			detail = "executable"
		}
	}
	checks := []Check{{Name: "binary", OK: binOK, Detail: detail}, {Name: "config", OK: in.ConfigOK, Detail: in.ConfigDetail}, {Name: "models", OK: in.ModelsOK, Detail: in.ModelsDetail}, {Name: "llama_cpp_libs", OK: in.LibsOK, Detail: in.LibsDetail}, {Name: "service_installed", OK: in.ServiceInstalled}, {Name: "server_health", OK: in.ServerHealthy, Detail: in.ServerDetail}, {Name: "model_status", OK: in.ServerHealthy && (in.ModelStatus == "loaded" || in.ModelStatus == "loading" || in.ModelStatus == "unloaded" || in.ModelStatus == "agent"), Detail: in.ModelStatus}, {Name: "model_idle_seconds", OK: in.ServerHealthy && in.IdleSeconds >= 0, Detail: strconv.Itoa(in.IdleSeconds)}, {Name: "hooks", OK: in.HooksOK, Detail: in.HookDetail}, {Name: "latency", OK: in.ServerHealthy && in.Latency >= 0, Detail: in.Latency.String()}}
	if !in.ServiceInstalled {
		checks[4].Detail = "user service is not installed"
	}
	if !in.ServiceActive {
		checks = append(checks, Check{Name: "service_active", OK: false, Detail: "user service is not active"})
	} else {
		checks = append(checks, Check{Name: "service_active", OK: true})
	}
	return checks
}

// OK reports whether every check passed.
func OK(checks []Check) bool {
	for _, c := range checks {
		if !c.OK {
			return false
		}
	}
	return true
}
