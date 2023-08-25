package dht

import (
	"context"

	ma "github.com/multiformats/go-multiaddr"
	"github.com/plprobelab/go-kademlia/kad"
	"github.com/plprobelab/go-kademlia/key"
	"github.com/plprobelab/go-kademlia/network/address"
	"github.com/plprobelab/go-kademlia/query"
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

type (
	// TODO: these behaviours can be parameterised by more specific types
	NetworkBehaviour Behaviour[DhtEvent, DhtEvent]
	RoutingBehaviour Behaviour[DhtEvent, DhtEvent]
	QueryBehaviour   Behaviour[DhtEvent, DhtEvent]
)

// A NullBehaviour is never ready, ignores all notifications and never performs any work, much like a teenager.
type NullBehaviour struct{}

func (NullBehaviour) Ready() <-chan struct{}                       { return nil }
func (NullBehaviour) Notify(ctx context.Context, ev DhtEvent)      {}
func (NullBehaviour) Perform(ctx context.Context) (DhtEvent, bool) { return nil, false }

// A Coordinator is responsible for coordinating events between behaviours
type Coordinator struct {
	// networkBehaviour is the behaviour responsible for communicating with the network
	networkBehaviour NetworkBehaviour

	// routingBehaviour is the behaviour responsible for maintaining the routing table
	routingBehaviour RoutingBehaviour

	// queryBehaviour is the behaviour responsible for running user-submitted queries
	queryBehaviour QueryBehaviour

	// cancel is a function that cancels the event loop
	cancel context.CancelFunc
}

func NewCoordinator() *Coordinator {
	ctx, cancel := context.WithCancel(context.Background())

	// TODO: set the behaviours!
	c := &Coordinator{
		networkBehaviour: new(NullBehaviour),
		routingBehaviour: new(NullBehaviour),
		queryBehaviour:   new(NullBehaviour),
		cancel:           cancel,
	}
	go c.eventLoop(ctx)
	return c
}

func (d *Coordinator) eventLoop(ctx context.Context) {
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
func (d *Coordinator) Close() {
	if d.cancel != nil {
		d.cancel()
	}
}

// Dispatch routes an event to the correct behaviour.
func (c *Coordinator) Dispatch(ctx context.Context, ev DhtEvent) {
	switch ev := ev.(type) {
	case *EventDhtStartBootstrap:
		c.routingBehaviour.Notify(ctx, ev)
	case *EventOutboundGetClosestNodes:
		c.networkBehaviour.Notify(ctx, ev)
	case *EventStartQuery:
		c.queryBehaviour.Notify(ctx, ev)
	case *EventStopQuery:
		c.queryBehaviour.Notify(ctx, ev)
	case *EventDhtAddNodeInfo:
		c.routingBehaviour.Notify(ctx, ev)
	case RoutingNotification:
		// select {
		// case <-ctx.Done():
		// case c.routingNotifications <- ev:
		// default:
		// }
	}
}

type Notify[C DhtEvent] interface {
	Notify(ctx context.Context, ev C)
}

type NotifyCloser[C DhtEvent] interface {
	Notify(ctx context.Context, ev C)
	Close()
}

type NotifyFunc[C DhtEvent] func(ctx context.Context, ev C)

func (f NotifyFunc[C]) Notify(ctx context.Context, ev C) {
	f(ctx, ev)
}

type DhtEvent interface {
	dhtEvent()
}

type DhtCommand interface {
	DhtEvent
	dhtCommand()
}

type NodeHandlerRequest interface {
	DhtEvent
	nodeHandlerRequest()
}

type NodeHandlerResponse interface {
	DhtEvent
	nodeHandlerResponse()
}

type RoutingNotification interface {
	DhtEvent
	routingNotificationEvent()
}

type EventDhtStartBootstrap struct {
	ProtocolID address.ProtocolID
	Message    kad.Request[key.Key256, ma.Multiaddr]
	SeedNodes  []nodeID // TODO: nodeID or peer.ID or kad.NodeID[key.Key256]
}

func (EventDhtStartBootstrap) dhtEvent()   {}
func (EventDhtStartBootstrap) dhtCommand() {}

type EventOutboundGetClosestNodes struct {
	QueryID  query.QueryID
	To       nodeInfo // TODO: nodeInfo or peer.AddrInfo or kad.NodeInfo[key.Key256, ma.Multiaddr]
	Target   key.Key256
	Notifiee Notify[DhtEvent]
}

func (EventOutboundGetClosestNodes) dhtEvent()           {}
func (EventOutboundGetClosestNodes) nodeHandlerRequest() {}

type EventStartQuery struct {
	QueryID           query.QueryID
	Target            key.Key256
	ProtocolID        address.ProtocolID
	Message           kad.Request[key.Key256, ma.Multiaddr]
	KnownClosestNodes []nodeID // TODO: nodeID or peer.ID or kad.NodeID[key.Key256]
	Waiter            NotifyCloser[DhtEvent]
}

func (EventStartQuery) dhtEvent()   {}
func (EventStartQuery) dhtCommand() {}

type EventStopQuery struct {
	QueryID query.QueryID
}

func (EventStopQuery) dhtEvent()   {}
func (EventStopQuery) dhtCommand() {}

type EventDhtAddNodeInfo struct {
	NodeInfo nodeInfo // TODO: nodeInfo or peer.AddrInfo or kad.NodeInfo[key.Key256, ma.Multiaddr]
}

func (EventDhtAddNodeInfo) dhtEvent()   {}
func (EventDhtAddNodeInfo) dhtCommand() {}

type EventGetClosestNodesSuccess struct {
	QueryID      query.QueryID
	To           nodeInfo // TODO: nodeInfo or peer.AddrInfo or kad.NodeInfo[key.Key256, ma.Multiaddr]
	Target       key.Key256
	ClosestNodes []nodeInfo // TODO: nodeInfo or peer.AddrInfo or kad.NodeInfo[key.Key256, ma.Multiaddr]
}

func (EventGetClosestNodesSuccess) dhtEvent()            {}
func (EventGetClosestNodesSuccess) nodeHandlerResponse() {}

type EventGetClosestNodesFailure struct {
	QueryID query.QueryID
	To      nodeInfo // TODO: nodeInfo or peer.AddrInfo or kad.NodeInfo[key.Key256, ma.Multiaddr]
	Target  key.Key256
	Err     error
}

func (EventGetClosestNodesFailure) dhtEvent()            {}
func (EventGetClosestNodesFailure) nodeHandlerResponse() {}

// EventQueryProgressed is emitted by the dht when a query has received a
// response from a node.
type EventQueryProgressed struct {
	QueryID  query.QueryID
	NodeID   nodeID // TODO: nodeID or peer.ID or kad.NodeID[key.Key256]
	Response kad.Response[key.Key256, ma.Multiaddr]
	Stats    query.QueryStats
}

func (*EventQueryProgressed) dhtEvent() {}

// EventQueryFinished is emitted by the dht when a query has finished, either through
// running to completion or by being canceled.
type EventQueryFinished struct {
	QueryID query.QueryID
	Stats   query.QueryStats
}

func (*EventQueryFinished) dhtEvent() {}

// EventRoutingUpdated is emitted by the dht when a new node has been verified and added to the routing table.
type EventRoutingUpdated struct {
	NodeInfo nodeInfo // TODO: nodeInfo or peer.AddrInfo or kad.NodeInfo[key.Key256, ma.Multiaddr]
}

func (*EventRoutingUpdated) dhtEvent()                 {}
func (*EventRoutingUpdated) routingNotificationEvent() {}

// EventBootstrapFinished is emitted by the dht when a bootstrap has finished, either through
// running to completion or by being canceled.
type EventBootstrapFinished struct {
	Stats query.QueryStats
}

func (*EventBootstrapFinished) dhtEvent()                 {}
func (*EventBootstrapFinished) routingNotificationEvent() {}
