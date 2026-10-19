// Package registry lists the host adapters.
package registry

import (
	"fmt"
	"strings"

	"github.com/MQ37/nod/internal/hosts"
	"github.com/MQ37/nod/internal/hosts/claude"
	"github.com/MQ37/nod/internal/hosts/hermes"
	"github.com/MQ37/nod/internal/hosts/opencode"
	"github.com/MQ37/nod/internal/hosts/pi"
)

// Names in display order.
var Names = []string{claude.Name, hermes.Name, pi.Name, opencode.Name}

// Get returns the adapter named name.
func Get(env hosts.Env, name string) (hosts.Host, error) {
	switch strings.ToLower(name) {
	case claude.Name, "claude-code":
		return claude.New(env), nil
	case hermes.Name:
		return hermes.New(env), nil
	case pi.Name, "omp":
		return pi.New(env), nil
	case opencode.Name:
		return opencode.New(env), nil
	}
	return nil, fmt.Errorf("unknown host %q (choose from %s)", name, strings.Join(Names, ", "))
}

// All returns every adapter.
func All(env hosts.Env) []hosts.Host {
	out := make([]hosts.Host, 0, len(Names))
	for _, n := range Names {
		h, _ := Get(env, n)
		out = append(out, h)
	}
	return out
}
