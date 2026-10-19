// Package power answers one question: is this machine running on battery? Nod uses it to keep
// the local model from loading on a laptop that is not plugged in.
package power

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// State is what we could learn about the machine's power source.
type State struct {
	// Known is false when no power information exists (a desktop, a container, an unsupported OS).
	Known bool
	// HasBattery reports whether a battery was found.
	HasBattery bool
	// OnAC reports whether mains power is connected. Meaningful only when Known.
	OnAC bool
}

// OnBattery reports a laptop that is running on its battery. Unknown power counts as plugged in:
// a machine with no battery information is almost always a desktop or a server.
func (s State) OnBattery() bool { return s.Known && s.HasBattery && !s.OnAC }

// Reader reads the current state.
type Reader func() State

// ErrOnBattery and ErrDisabled are the reasons Allowed refuses; both wrap into decider.ErrPaused upstream.
var (
	ErrOnBattery = errors.New("on battery")
	ErrDisabled  = errors.New("disabled by local_power=never")
)

// Allowed applies a local_power policy (always | plugged | never) to a state.
func Allowed(policy string, s State) error {
	switch policy {
	case "never":
		return ErrDisabled
	case "always":
		return nil
	default: // "plugged" and anything unknown: fail towards saving power
		if s.OnBattery() {
			return ErrOnBattery
		}
		return nil
	}
}

// Cached wraps a reader so the (possibly subprocess-backed) read happens at most once per ttl.
func Cached(r Reader, ttl time.Duration, now func() time.Time) Reader {
	if now == nil {
		now = time.Now
	}
	var mu sync.Mutex
	var at time.Time
	var last State
	return func() State {
		mu.Lock()
		defer mu.Unlock()
		if !at.IsZero() && now().Sub(at) < ttl {
			return last
		}
		last = r()
		at = now()
		return last
	}
}

// Detect reads the power state of this machine.
func Detect(goos string) State {
	switch goos {
	case "linux":
		return ReadSysfs("/sys/class/power_supply")
	case "darwin":
		return ReadPmset(func() (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, "/usr/bin/pmset", "-g", "batt").Output()
			return string(out), err
		})
	}
	return State{}
}

// ReadSysfs reads Linux power_supply entries under dir. A Mains, USB or Wireless supply with
// online=1 means plugged in; a supply of type Battery means a laptop. (A battery that says "Not
// charging" while AC is online, for example because of a charge limit, is still plugged in.)
func ReadSysfs(dir string) State {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return State{}
	}
	var st State
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		typ := strings.TrimSpace(readFile(filepath.Join(p, "type")))
		switch typ {
		case "Battery":
			// Peripherals (mice, headsets) also show up as batteries; they declare scope=Device.
			if strings.TrimSpace(readFile(filepath.Join(p, "scope"))) == "Device" {
				continue
			}
			st.Known, st.HasBattery = true, true
		case "Mains", "USB", "Wireless", "USB_PD", "USB_PD_DRP", "USB_C", "USB_DCP", "USB_CDP", "USB_ACA":
			st.Known = true
			if strings.TrimSpace(readFile(filepath.Join(p, "online"))) == "1" {
				st.OnAC = true
			}
		}
	}
	return st
}

// ReadPmset parses `pmset -g batt` output.
func ReadPmset(run func() (string, error)) State {
	out, err := run()
	if err != nil {
		return State{}
	}
	switch {
	case strings.Contains(out, "'AC Power'"):
		return State{Known: true, HasBattery: strings.Contains(out, "InternalBattery"), OnAC: true}
	case strings.Contains(out, "'Battery Power'"), strings.Contains(out, "'UPS Power'"):
		return State{Known: true, HasBattery: true}
	}
	return State{}
}

func readFile(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

// Describe is a short human string for status output.
func (s State) Describe() string {
	switch {
	case !s.Known:
		return "no power information (treated as plugged in)"
	case !s.HasBattery:
		return "no battery (plugged in)"
	case s.OnAC:
		return "plugged in"
	}
	return "on battery"
}
