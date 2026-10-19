package decider

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestManagedFailedLoadDoesNotRetryRecursively(t *testing.T) {
	calls := 0
	m := NewManaged(func(context.Context) (ClosableDecider, error) { calls++; return nil, errors.New("unavailable") }, time.Second, time.Minute)
	_, err := m.Decide("", Schema{})
	if !errors.Is(err, ErrWarmingUp) {
		t.Fatalf("err=%v", err)
	}
	if calls != 1 {
		t.Fatalf("loader called %d times", calls)
	}
}
