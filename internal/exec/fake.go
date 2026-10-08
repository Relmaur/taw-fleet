package exec

import (
	"context"
	"sync"
)

// FakeRunner records every command and answers with Script. With no Script,
// every command succeeds with empty output.
type FakeRunner struct {
	Script func(Spec) (Result, error)

	mu    sync.Mutex
	calls []Spec
}

// Run implements Runner.
func (f *FakeRunner) Run(ctx context.Context, s Spec) (Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, s)
	f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if f.Script == nil {
		return Result{}, nil
	}
	return f.Script(s)
}

// Calls returns a copy of the commands run so far.
func (f *FakeRunner) Calls() []Spec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Spec(nil), f.calls...)
}
