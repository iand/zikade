package coord

import (
	"context"
	"testing"
	"time"
)

type RecordingSM[E any, S any] struct {
	State    S
	Received []E
}

func NewRecordingSM[E any, S any](response S) *RecordingSM[E, S] {
	return &RecordingSM[E, S]{
		State: response,
	}
}

func (r *RecordingSM[E, S]) Advance(ctx context.Context, now time.Time, e E) S {
	r.Received = append(r.Received, e)
	return r.State
}

func (r *RecordingSM[E, S]) first() E {
	if len(r.Received) == 0 {
		var zero E
		return zero
	}
	return r.Received[0]
}

// maxPerformIterations bounds [PerformWhileReady] so that a behaviour which
// signals ready without ever running out of work fails the test instead of
// hanging it.
const maxPerformIterations = 1000

// PerformWhileReady drives a behaviour the way [Coordinator.eventLoop] drives
// it: Perform is called only while Ready() is signalled, never speculatively.
// It returns the events the behaviour emitted, and stops once the behaviour
// stops signalling that it has work to do.
func PerformWhileReady[I BehaviourEvent, O BehaviourEvent](t *testing.T, ctx context.Context, b Behaviour[I, O]) []O {
	t.Helper()

	var evs []O
	for range maxPerformIterations {
		select {
		case <-b.Ready():
			ev, ok := b.Perform(ctx)
			if ok {
				evs = append(evs, ev)
			}
		case <-ctx.Done():
			t.Fatal("context cancelled while performing behaviour work")
		default:
			return evs
		}
	}

	t.Fatalf("behaviour still ready after %d iterations", maxPerformIterations)
	return nil
}

func DrainBehaviour[I BehaviourEvent, O BehaviourEvent](t *testing.T, ctx context.Context, b Behaviour[I, O]) {
	for {
		select {
		case <-b.Ready():
			b.Perform(ctx)
		case <-ctx.Done():
			t.Fatal("context cancelled while draining behaviour")
		default:
			return
		}
	}
}
