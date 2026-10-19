package decider

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ClosableDecider is a loaded model that can release native llama.cpp state.
type ClosableDecider interface {
	Decider
	Close() error
}

// Loader loads a model and its native resources.
type Loader func(context.Context) (ClosableDecider, error)

type State struct {
	Status      string `json:"status"`
	IdleSeconds int64  `json:"idle_seconds"`
	// Paused is the reason the model will not load right now (empty when it may).
	Paused string `json:"paused,omitempty"`
}

// Managed lazily loads a single decider and safely unloads it after inactivity.
type Managed struct {
	mu         sync.Mutex
	loader     Loader
	warmWait   time.Duration
	idle       time.Duration
	now        func() time.Time
	model      ClosableDecider
	loadErr    error
	loading    bool
	loadDone   chan struct{}
	active     int
	unloading  bool
	unloadDone chan struct{}
	lastUse    time.Time
	timer      *time.Timer
	gate       func() error
}

func NewManaged(loader Loader, warmWait time.Duration, idle time.Duration) *Managed {
	return &Managed{loader: loader, warmWait: warmWait, idle: idle, now: time.Now}
}

// SetGate installs a check that must pass before the model may be loaded or used. A failing gate
// (say, on battery) makes Decide return an error that wraps ErrPaused and unloads an idle model.
// The gate must be cheap or cache its answer. Call before use.
func (m *Managed) SetGate(g func() error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gate = g
}

// gateErr evaluates the gate outside the lock; the result wraps ErrPaused.
func (m *Managed) gateErr() error {
	m.mu.Lock()
	g := m.gate
	m.mu.Unlock()
	if g == nil {
		return nil
	}
	if err := g(); err != nil {
		return fmt.Errorf("%w: %v", ErrPaused, err)
	}
	return nil
}

// unloadForGate frees an idle loaded model regardless of the idle timer.
func (m *Managed) unloadForGate() {
	m.mu.Lock()
	if m.model == nil || m.loading || m.active != 0 {
		m.mu.Unlock()
		return
	}
	model := m.model
	m.model = nil
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	m.unloading = true
	m.unloadDone = make(chan struct{})
	done := m.unloadDone
	m.mu.Unlock()
	_ = model.Close()
	m.mu.Lock()
	m.unloading = false
	close(done)
	m.mu.Unlock()
}

// Watch re-checks the gate every interval until ctx ends and unloads an idle model whose gate
// fails (the laptop was unplugged). In-flight calls finish first.
func (m *Managed) Watch(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if m.gateErr() != nil {
				m.unloadForGate()
			}
		}
	}
}

// SetClock replaces the clock for deterministic lifecycle tests; call before use.
func (m *Managed) SetClock(now func() time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if now != nil {
		m.now = now
	}
}

func (m *Managed) startLoad() {
	m.loading = true
	m.loadDone = make(chan struct{})
	done := m.loadDone
	go func() {
		model, err := m.loader(context.Background())
		m.mu.Lock()
		if err == nil && model == nil {
			err = errors.New("loader returned nil model")
		}
		m.loadErr = err
		if err == nil {
			m.model = model
			m.lastUse = m.now()
		}
		m.loading = false
		close(done)
		m.mu.Unlock()
	}()
}

// Warm starts loading in the background without waiting.
func (m *Managed) Warm() {
	if m.gateErr() != nil {
		m.unloadForGate()
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.model == nil && !m.loading && !m.unloading && m.loader != nil {
		m.startLoad()
	}
}

// Decide triggers a background load and waits no longer than warmWait.
func (m *Managed) Decide(text string, schema Schema) (map[string]Answer, error) {
	if err := m.gateErr(); err != nil {
		m.unloadForGate()
		return nil, err
	}
	m.mu.Lock()
	if m.model == nil {
		if m.unloading {
			done := m.unloadDone
			m.mu.Unlock()
			t := time.NewTimer(m.warmWait)
			defer t.Stop()
			select {
			case <-done:
				return m.Decide(text, schema)
			case <-t.C:
				return nil, ErrWarmingUp
			}
		}
		if !m.loading {
			if m.loader == nil {
				m.mu.Unlock()
				return nil, ErrWarmingUp
			}
			m.startLoad()
		}
		done := m.loadDone
		m.mu.Unlock()
		if m.warmWait <= 0 {
			return nil, ErrWarmingUp
		}
		t := time.NewTimer(m.warmWait)
		defer t.Stop()
		select {
		case <-done:
		case <-t.C:
			return nil, ErrWarmingUp
		}
		m.mu.Lock()
		loaded := m.model != nil
		m.mu.Unlock()
		if !loaded {
			return nil, ErrWarmingUp
		}
		return m.Decide(text, schema)
	}
	m.active++
	model := m.model
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	m.mu.Unlock()
	ans, err := model.Decide(text, schema)
	m.mu.Lock()
	m.active--
	m.lastUse = m.now()
	if m.active == 0 {
		m.armLocked()
	}
	m.mu.Unlock()
	return ans, err
}

func (m *Managed) armLocked() {
	if m.idle <= 0 || m.model == nil || m.active != 0 || m.loading {
		return
	}
	if m.timer != nil {
		m.timer.Stop()
	}
	m.timer = time.AfterFunc(m.idle, func() { m.UnloadIfIdle() })
}

// UnloadIfIdle frees native resources once the configured idle duration elapsed.
func (m *Managed) UnloadIfIdle() bool {
	m.mu.Lock()
	if m.model == nil || m.loading || m.active != 0 || m.idle <= 0 || m.now().Sub(m.lastUse) < m.idle {
		m.mu.Unlock()
		return false
	}
	model := m.model
	m.model = nil
	m.timer = nil
	m.unloading = true
	m.unloadDone = make(chan struct{})
	done := m.unloadDone
	m.mu.Unlock()
	_ = model.Close()
	m.mu.Lock()
	m.unloading = false
	close(done)
	m.mu.Unlock()
	return true
}

// Status reports loaded, loading or unloaded and idle seconds.
func (m *Managed) Status() State {
	paused := ""
	if err := m.gateErr(); err != nil {
		paused = err.Error()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	status := "unloaded"
	var idle int64
	if m.loading {
		status = "loading"
	} else if m.model != nil {
		status = "loaded"
		idle = int64(m.now().Sub(m.lastUse).Seconds())
		if idle < 0 {
			idle = 0
		}
	}
	return State{Status: status, IdleSeconds: idle, Paused: paused}
}
