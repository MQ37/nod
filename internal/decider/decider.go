package decider

import (
	"context"
	"errors"
)

// Decider evaluates a schema against context text.
type Decider interface {
	Decide(context string, schema Schema) (map[string]Answer, error)
}

// Fake is a deterministic decider for tests and installations without a model.
type Fake struct {
	Answers map[string]Answer
	Err     error
}

func (f Fake) Decide(_ string, _ Schema) (map[string]Answer, error) { return f.Answers, f.Err }

// ContextDecider is the context-aware form used by server requests.
type ContextDecider interface {
	DecideContext(context.Context, string, Schema) (map[string]Answer, error)
}

// ErrWarmingUp reports that a cold model exceeded its bounded warm wait.
var ErrWarmingUp = errors.New("model warming up")

// ErrPaused reports that the model is deliberately not loaded (for example on battery power). The
// wrapping error carries the reason; callers answer ask and say why.
var ErrPaused = errors.New("local model paused")
