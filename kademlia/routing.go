package kademlia

import (
	"context"
	"fmt"
	"sync"

	"github.com/plprobelab/go-kademlia/kad"
	"github.com/plprobelab/go-kademlia/key"
	"github.com/plprobelab/go-kademlia/routing"
	"github.com/plprobelab/go-kademlia/util"
	"golang.org/x/exp/slog"
)

type RoutingBehaviour[K kad.Key[K], A kad.Address[A]] struct {
	// self is the node id of the system the dht is running on
	self kad.NodeID[K]
	// bootstrap is the bootstrap state machine, responsible for bootstrapping the routing table
	bootstrap *routing.Bootstrap[K, A]
	include   *routing.Include[K, A]

	pendingMu sync.Mutex
	pending   []DhtEvent
	ready     chan struct{}

	logger *slog.Logger
}

func NewRoutingBehaviour[K kad.Key[K], A kad.Address[A]](self kad.NodeID[K], bootstrap *routing.Bootstrap[K, A], include *routing.Include[K, A], logger *slog.Logger) *RoutingBehaviour[K, A] {
	r := &RoutingBehaviour[K, A]{
		self:      self,
		bootstrap: bootstrap,
		include:   include,
		ready:     make(chan struct{}, 1),
		logger:    logger,
	}
	return r
}

func (r *RoutingBehaviour[K, A]) Notify(ctx context.Context, ev DhtEvent) {
	ctx, span := util.StartSpan(ctx, "RoutingBehaviour.Perform")
	defer span.End()

	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()

	switch ev := ev.(type) {
	case *EventDhtStartBootstrap[K, A]:
		cmd := &routing.EventBootstrapStart[K, A]{
			ProtocolID:        ev.ProtocolID,
			Message:           ev.Message,
			KnownClosestNodes: ev.SeedNodes,
		}
		// attempt to advance the bootstrap
		next, ok := r.advanceBootstrap(ctx, cmd)
		if ok {
			r.pending = append(r.pending, next)
		}

	case *EventDhtAddNodeInfo[K, A]:
		// Ignore self
		if key.Equal(ev.NodeInfo.ID().Key(), r.self.Key()) {
			break
		}
		cmd := &routing.EventIncludeAddCandidate[K, A]{
			NodeInfo: ev.NodeInfo,
		}
		// attempt to advance the include
		next, ok := r.advanceInclude(ctx, cmd)
		if ok {
			r.pending = append(r.pending, next)
		}

	case *EventGetClosestNodesSuccess[K, A]:
		switch ev.QueryID {
		case "bootstrap":
			for _, info := range ev.ClosestNodes {
				// TODO: do this after advancing bootstrap
				r.pending = append(r.pending, &EventDhtAddNodeInfo[K, A]{
					NodeInfo: info,
				})
			}
			cmd := &routing.EventBootstrapMessageResponse[K, A]{
				NodeID:   ev.To.ID(),
				Response: ClosestNodesFakeResponse(ev.Target, ev.ClosestNodes),
			}
			// attempt to advance the bootstrap
			next, ok := r.advanceBootstrap(ctx, cmd)
			if ok {
				r.pending = append(r.pending, next)
			}

		case "include":
			cmd := &routing.EventIncludeMessageResponse[K, A]{
				NodeInfo: ev.To,
				Response: ClosestNodesFakeResponse(ev.Target, ev.ClosestNodes),
			}
			// attempt to advance the include
			next, ok := r.advanceInclude(ctx, cmd)
			if ok {
				r.pending = append(r.pending, next)
			}

		default:
			panic(fmt.Sprintf("unexpected query id: %s", ev.QueryID))
		}
	case *EventGetClosestNodesFailure[K, A]:
		switch ev.QueryID {
		case "bootstrap":
			cmd := &routing.EventBootstrapMessageFailure[K]{
				NodeID: ev.To.ID(),
				Error:  ev.Err,
			}
			// attempt to advance the bootstrap
			next, ok := r.advanceBootstrap(ctx, cmd)
			if ok {
				r.pending = append(r.pending, next)
			}
		case "include":
			cmd := &routing.EventIncludeMessageFailure[K, A]{
				NodeInfo: ev.To,
				Error:    ev.Err,
			}
			// attempt to advance the include
			next, ok := r.advanceInclude(ctx, cmd)
			if ok {
				r.pending = append(r.pending, next)
			}

		default:
			panic(fmt.Sprintf("unexpected query id: %s", ev.QueryID))
		}
	default:
		panic(fmt.Sprintf("unexpected dht event: %T", ev))
	}

	if len(r.pending) > 0 {
		select {
		case r.ready <- struct{}{}:
		default:
		}
	}
}

func (r *RoutingBehaviour[K, A]) Ready() <-chan struct{} {
	return r.ready
}

func (r *RoutingBehaviour[K, A]) Perform(ctx context.Context) (DhtEvent, bool) {
	ctx, span := util.StartSpan(ctx, "RoutingBehaviour.Perform")
	defer span.End()

	// No inbound work can be done until Perform is complete
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()

	for {
		// drain queued events first.
		if len(r.pending) > 0 {
			var ev DhtEvent
			ev, r.pending = r.pending[0], r.pending[1:]

			if len(r.pending) > 0 {
				select {
				case r.ready <- struct{}{}:
				default:
				}
			}
			return ev, true
		}

		// attempt to advance the bootstrap state machine
		bstate := r.bootstrap.Advance(ctx, &routing.EventBootstrapPoll{})
		switch st := bstate.(type) {

		case *routing.StateBootstrapMessage[K, A]:
			return &EventOutboundGetClosestNodes[K, A]{
				QueryID:  "bootstrap",
				To:       NewNodeAddr[K, A](st.NodeID, nil),
				Target:   st.Message.Target(),
				Notifiee: r,
			}, true

		case *routing.StateBootstrapWaiting:
			// bootstrap waiting for a message response, nothing to do
		case *routing.StateBootstrapFinished:
			return &EventBootstrapFinished{
				Stats: st.Stats,
			}, true
		case *routing.StateBootstrapIdle:
			// bootstrap not running, nothing to do
		default:
			panic(fmt.Sprintf("unexpected bootstrap state: %T", st))
		}

		// attempt to advance the include state machine
		istate := r.include.Advance(ctx, &routing.EventIncludePoll{})
		switch st := istate.(type) {
		case *routing.StateIncludeFindNodeMessage[K, A]:
			// include wants to send a find node message to a node
			return &EventOutboundGetClosestNodes[K, A]{
				QueryID:  "include",
				To:       st.NodeInfo,
				Target:   st.NodeInfo.ID().Key(),
				Notifiee: r,
			}, true

		case *routing.StateIncludeRoutingUpdated[K, A]:
			// a node has been included in the routing table
			return &EventRoutingUpdated[K, A]{
				NodeInfo: st.NodeInfo,
			}, true
		case *routing.StateIncludeWaitingAtCapacity:
			// nothing to do except wait for message response or timeout
		case *routing.StateIncludeWaitingWithCapacity:
			// nothing to do except wait for message response or timeout
		case *routing.StateIncludeWaitingFull:
			// nothing to do except wait for message response or timeout
		case *routing.StateIncludeIdle:
			// nothing to do except wait for message response or timeout
		default:
			panic(fmt.Sprintf("unexpected include state: %T", st))
		}

		if len(r.pending) == 0 {
			return nil, false
		}
	}
}

func (r *RoutingBehaviour[K, A]) advanceBootstrap(ctx context.Context, ev routing.BootstrapEvent) (DhtEvent, bool) {
	ctx, span := util.StartSpan(ctx, "RoutingBehaviour.advanceBootstrap")
	defer span.End()
	bstate := r.bootstrap.Advance(ctx, ev)
	switch st := bstate.(type) {

	case *routing.StateBootstrapMessage[K, A]:
		return &EventOutboundGetClosestNodes[K, A]{
			QueryID:  "bootstrap",
			To:       NewNodeAddr[K, A](st.NodeID, nil),
			Target:   st.Message.Target(),
			Notifiee: r,
		}, true

	case *routing.StateBootstrapWaiting:
		// bootstrap waiting for a message response, nothing to do
	case *routing.StateBootstrapFinished:
		return &EventBootstrapFinished{
			Stats: st.Stats,
		}, true
	case *routing.StateBootstrapIdle:
		// bootstrap not running, nothing to do
	default:
		panic(fmt.Sprintf("unexpected bootstrap state: %T", st))
	}

	return nil, false
}

func (r *RoutingBehaviour[K, A]) advanceInclude(ctx context.Context, ev routing.IncludeEvent) (DhtEvent, bool) {
	ctx, span := util.StartSpan(ctx, "RoutingBehaviour.advanceInclude")
	defer span.End()
	istate := r.include.Advance(ctx, ev)
	switch st := istate.(type) {
	case *routing.StateIncludeFindNodeMessage[K, A]:
		// include wants to send a find node message to a node
		return &EventOutboundGetClosestNodes[K, A]{
			QueryID:  "include",
			To:       st.NodeInfo,
			Target:   st.NodeInfo.ID().Key(),
			Notifiee: r,
		}, true

	case *routing.StateIncludeRoutingUpdated[K, A]:
		// a node has been included in the routing table
		return &EventRoutingUpdated[K, A]{
			NodeInfo: st.NodeInfo,
		}, true
	case *routing.StateIncludeWaitingAtCapacity:
		// nothing to do except wait for message response or timeout
	case *routing.StateIncludeWaitingWithCapacity:
		// nothing to do except wait for message response or timeout
	case *routing.StateIncludeWaitingFull:
		// nothing to do except wait for message response or timeout
	case *routing.StateIncludeIdle:
		// nothing to do except wait for message response or timeout
	default:
		panic(fmt.Sprintf("unexpected include state: %T", st))
	}

	return nil, false
}
