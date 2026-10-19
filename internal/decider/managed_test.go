package decider

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeLoaded struct {
	closes atomic.Int32
	calls  atomic.Int32
}

func (f *fakeLoaded) Decide(string, Schema) (map[string]Answer, error) {
	f.calls.Add(1)
	return map[string]Answer{}, nil
}
func (f *fakeLoaded) Close() error { f.closes.Add(1); return nil }

func TestManagedSlowLoadTimesOutAndCanBeWarmed(t *testing.T) {
	gate := make(chan struct{})
	var loads atomic.Int32
	model := &fakeLoaded{}
	m := NewManaged(func(context.Context) (ClosableDecider, error) { loads.Add(1); <-gate; return model, nil }, 10*time.Millisecond, time.Hour)
	_, err := m.Decide("", Schema{})
	if !errors.Is(err, ErrWarmingUp) {
		t.Fatalf("err=%v", err)
	}
	if got := m.Status().Status; got != "loading" {
		t.Fatalf("status=%s", got)
	}
	close(gate)
	deadline := time.Now().Add(time.Second)
	for m.Status().Status != "loaded" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := loads.Load(); got != 1 {
		t.Fatalf("loads=%d", got)
	}
	if _, err = m.Decide("", Schema{}); err != nil {
		t.Fatal(err)
	}
}

func TestManagedConcurrentCallsLoadOnce(t *testing.T) {
	var loads atomic.Int32
	model := &fakeLoaded{}
	m := NewManaged(func(context.Context) (ClosableDecider, error) {
		loads.Add(1)
		time.Sleep(15 * time.Millisecond)
		return model, nil
	}, time.Second, time.Hour)
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.Decide("", Schema{}); err != nil {
				t.Errorf("Decide: %v", err)
			}
		}()
	}
	wg.Wait()
	if loads.Load() != 1 {
		t.Fatalf("load calls=%d", loads.Load())
	}
	if model.calls.Load() != 12 {
		t.Fatalf("decide calls=%d", model.calls.Load())
	}
}

func TestManagedUnloadClosesNativeState(t *testing.T) {
	now := time.Unix(100, 0)
	model := &fakeLoaded{}
	m := NewManaged(func(context.Context) (ClosableDecider, error) { return model, nil }, time.Second, 30*time.Second)
	m.SetClock(func() time.Time { return now })
	if _, err := m.Decide("", Schema{}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(31 * time.Second)
	if !m.UnloadIfIdle() {
		t.Fatal("expected unload")
	}
	if model.closes.Load() != 1 {
		t.Fatalf("Close calls=%d", model.closes.Load())
	}
	if got := m.Status().Status; got != "unloaded" {
		t.Fatalf("status=%s", got)
	}
}

func TestManagedGateKeepsModelUnloadedOnBattery(t *testing.T) {
	var loads atomic.Int32
	model := &fakeLoaded{}
	m := NewManaged(func(context.Context) (ClosableDecider, error) { loads.Add(1); return model, nil }, time.Second, time.Hour)
	var onBattery atomic.Bool
	onBattery.Store(true)
	m.SetGate(func() error {
		if onBattery.Load() {
			return errors.New("on battery")
		}
		return nil
	})

	// On battery: never loads, says why, health reports it.
	if _, err := m.Decide("", Schema{}); !errors.Is(err, ErrPaused) {
		t.Fatalf("err=%v, want ErrPaused", err)
	}
	m.Warm()
	if loads.Load() != 0 {
		t.Fatalf("the model loaded %d times on battery", loads.Load())
	}
	if st := m.Status(); st.Status != "unloaded" || st.Paused == "" {
		t.Fatalf("status=%+v", st)
	}

	// Plugged in: loads and answers.
	onBattery.Store(false)
	if _, err := m.Decide("", Schema{}); err != nil {
		t.Fatal(err)
	}
	if loads.Load() != 1 || m.Status().Status != "loaded" || m.Status().Paused != "" {
		t.Fatalf("loads=%d status=%+v", loads.Load(), m.Status())
	}

	// Unplugged while loaded: the watcher frees the model.
	onBattery.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Watch(ctx, 5*time.Millisecond)
	deadline := time.Now().Add(time.Second)
	for model.closes.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if model.closes.Load() != 1 || m.Status().Status != "unloaded" {
		t.Fatalf("closes=%d status=%+v: unplugging must unload the model", model.closes.Load(), m.Status())
	}
}
