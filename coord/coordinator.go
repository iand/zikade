package coord

import (
	"context"

	"github.com/plprobelab/go-kademlia/kad"
)

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

// A NullBehaviour is never ready, ignores all notifications and never performs any work, much like a teenager.
type NullBehaviour struct{}

func (NullBehaviour) Ready() <-chan struct{}                       { return nil }
func (NullBehaviour) Notify(ctx context.Context, ev DhtEvent)      {}
func (NullBehaviour) Perform(ctx context.Context) (DhtEvent, bool) { return nil, false }

type (
	// TODO: these behaviours can be parameterised by more specific types
	NetworkBehaviour[K kad.Key[K], A kad.Address[A]] Behaviour[DhtEvent, DhtEvent]
	RoutingBehaviour[K kad.Key[K], A kad.Address[A]] Behaviour[DhtEvent, DhtEvent]
	QueryBehaviour[K kad.Key[K], A kad.Address[A]]   Behaviour[DhtEvent, DhtEvent]
)

// A Coordinator is responsible for coordinating events between behaviours
type Coordinator[K kad.Key[K], A kad.Address[A]] struct {
	// networkBehaviour is the behaviour responsible for communicating with the network
	networkBehaviour NetworkBehaviour[K, A]

	// routingBehaviour is the behaviour responsible for maintaining the routing table
	routingBehaviour RoutingBehaviour[K, A]

	// queryBehaviour is the behaviour responsible for running user-submitted queries
	queryBehaviour QueryBehaviour[K, A]

	// cancel is a function that cancels the event loop
	cancel context.CancelFunc
}

func NewCoordinator[K kad.Key[K], A kad.Address[A]](n NetworkBehaviour[K, A], r RoutingBehaviour[K, A], q QueryBehaviour[K, A]) *Coordinator[K, A] {
	ctx, cancel := context.WithCancel(context.Background())

	// TODO: set the behaviours!
	c := &Coordinator[K, A]{
		networkBehaviour: n,
		routingBehaviour: r,
		queryBehaviour:   q,
		cancel:           cancel,
	}
	go c.eventLoop(ctx)
	return c
}

func (d *Coordinator[K, A]) eventLoop(ctx context.Context) {
	for {
		var ev DhtEvent
		var ok bool
		select {
		case <-d.networkBehaviour.Ready():
			ev, ok = d.networkBehaviour.Perform(ctx)
		case <-d.routingBehaviour.Ready():
			ev, ok = d.routingBehaviour.Perform(ctx)
		case <-d.queryBehaviour.Ready():
			ev, ok = d.queryBehaviour.Perform(ctx)
		}

		if ok {
			d.Dispatch(ctx, ev)
		}
	}
}

// Close stops the coordinator and cancels the context used for calling behaviours.
func (d *Coordinator[K, A]) Close() {
	if d.cancel != nil {
		d.cancel()
	}
}

// Dispatch routes an event to the correct behaviour.
func (c *Coordinator[K, A]) Dispatch(ctx context.Context, ev DhtEvent) {
	switch ev := ev.(type) {
	case *EventDhtStartBootstrap[K, A]:
		c.routingBehaviour.Notify(ctx, ev)
	case *EventOutboundGetClosestNodes[K, A]:
		c.networkBehaviour.Notify(ctx, ev)
	case *EventStartQuery[K, A]:
		c.queryBehaviour.Notify(ctx, ev)
	case *EventStopQuery:
		c.queryBehaviour.Notify(ctx, ev)
	case *EventDhtAddNodeInfo[K, A]:
		c.routingBehaviour.Notify(ctx, ev)
	case RoutingNotification:
		// select {
		// case <-ctx.Done():
		// case c.routingNotifications <- ev:
		// default:
		// }
	}
}
