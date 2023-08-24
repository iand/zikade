package kademlia

import (
	"context"
	"sync"
	"sync/atomic"
)

type Notifiee[C DhtEvent] interface {
	Notify(ctx context.Context, ev C)
}

type NotifyFunc[C DhtEvent] func(ctx context.Context, ev C)

func (f NotifyFunc[C]) Notify(ctx context.Context, ev C) {
	f(ctx, ev)
}

type Behaviour[I DhtEvent, O DhtEvent] interface {
	// Ready returns a channel that signals when the behaviour is ready to perform work.
	Ready() <-chan struct{}

	// Notify informs the behaviour of an event. The behaviour may perform the event
	// immediately and queue the result, causing the behaviour to become ready.
	Notify(ctx context.Context, ev I)

	// Perform gives the behaviour the opportunity to perform work or to return a queued
	// result as an event.
	Perform(ctx context.Context) (O, bool)
}

type WorkQueueFunc[C any, E any] func(ctx context.Context, cmd C, out chan<- E) bool

type WorkQueue[C DhtEvent, E DhtEvent] struct {
	pending chan pendingCmd[C, E]
	fn      WorkQueueFunc[C, E]
	done    atomic.Bool
	once    sync.Once
}

func NewWorkQueue[C DhtEvent, E DhtEvent](fn WorkQueueFunc[C, E]) *WorkQueue[C, E] {
	w := &WorkQueue[C, E]{
		pending: make(chan pendingCmd[C, E], 16),
		fn:      fn,
	}
	return w
}

type pendingCmd[C any, E any] struct {
	Ctx context.Context
	Cmd C
	Out chan<- E
}

func (w *WorkQueue[C, E]) Enqueue(ctx context.Context, cmd C, out chan<- E) error {
	if w.done.Load() {
		return nil
	}
	w.once.Do(func() {
		go func() {
			defer w.done.Store(true)
			for cc := range w.pending {
				if cc.Ctx.Err() != nil {
					return
				}
				if done := w.fn(cc.Ctx, cc.Cmd, cc.Out); done {
					w.done.Store(true)
					return
				}
			}
		}()
	})

	select {
	case <-ctx.Done(): // this is the context for the work item
		return ctx.Err()
	case w.pending <- pendingCmd[C, E]{
		Ctx: ctx,
		Cmd: cmd,
		Out: out,
	}:
		return nil

	}
}

type Waiter[E DhtEvent] struct {
	pending chan WaiterEvent[E]
	done    atomic.Bool
}

func NewWaiter[E DhtEvent]() *Waiter[E] {
	w := &Waiter[E]{
		pending: make(chan WaiterEvent[E], 16),
	}
	return w
}

type WaiterEvent[E DhtEvent] struct {
	Ctx   context.Context
	Event E
}

func (w *Waiter[E]) Notify(ctx context.Context, ev E) {
	if w.done.Load() {
		return
	}
	select {
	case <-ctx.Done(): // this is the context for the work item
		return
	case w.pending <- WaiterEvent[E]{
		Ctx:   ctx,
		Event: ev,
	}:
		return

	}
}

func (w *Waiter[E]) Close() {
	w.done.Store(true)
	close(w.pending)
}

func (w *Waiter[E]) Chan() <-chan WaiterEvent[E] {
	return w.pending
}
