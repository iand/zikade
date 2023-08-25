package query

import (
	"context"
	"fmt"
	"sync"

	"github.com/plprobelab/go-kademlia/kad"
	"github.com/plprobelab/go-kademlia/query"
	"github.com/plprobelab/go-kademlia/util"

	"github.com/iand/zikade/coord"
	"github.com/iand/zikade/internal/shim"
)

type PooledQueryBehaviour[K kad.Key[K], A kad.Address[A]] struct {
	pool    *query.Pool[K, A]
	waiters map[query.QueryID]*coord.Waiter[coord.DhtEvent]

	pendingMu sync.Mutex
	pending   []coord.DhtEvent
	ready     chan struct{}

	cfg Config
}

func NewPooledQueryBehaviour[K kad.Key[K], A kad.Address[A]](self kad.NodeID[K], cfg *Config) (*PooledQueryBehaviour[K, A], error) {
	pool, err := query.NewPool[K, A](self, cfg.Pool)
	if err != nil {
		return nil, fmt.Errorf("query pool: %w", err)
	}

	h := &PooledQueryBehaviour[K, A]{
		pool:    pool,
		waiters: make(map[query.QueryID]*coord.Waiter[coord.DhtEvent]),
		ready:   make(chan struct{}, 1),
		cfg:     *cfg,
	}
	return h, nil
}

func (r *PooledQueryBehaviour[K, A]) Notify(ctx context.Context, ev coord.DhtEvent) {
	ctx, span := util.StartSpan(ctx, "PooledQueryBehaviour.Notify")
	defer span.End()

	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()

	var cmd query.PoolEvent
	switch ev := ev.(type) {
	case *coord.EventStartQuery[K, A]:
		cmd = &query.EventPoolAddQuery[K, A]{
			QueryID:           ev.QueryID,
			Target:            ev.Target,
			ProtocolID:        ev.ProtocolID,
			Message:           ev.Message,
			KnownClosestNodes: ev.KnownClosestNodes,
		}
		if ev.Waiter != nil {
			r.waiters[ev.QueryID] = ev.Waiter
		}

	case *coord.EventStopQuery:
		cmd = &query.EventPoolStopQuery{
			QueryID: ev.QueryID,
		}

	case *coord.EventGetClosestNodesSuccess[K, A]:
		for _, info := range ev.ClosestNodes {
			// TODO: do this after advancing pool
			r.pending = append(r.pending, &coord.EventDhtAddNodeInfo[K, A]{
				NodeInfo: info,
			})
		}
		waiter, ok := r.waiters[ev.QueryID]
		if ok {
			waiter.Notify(ctx, &coord.EventQueryProgressed[K, A]{
				NodeID:   ev.To.ID(),
				QueryID:  ev.QueryID,
				Response: shim.ClosestNodesFakeResponse(ev.Target, ev.ClosestNodes),
				// Stats:    stats,
			})
		}
		cmd = &query.EventPoolMessageResponse[K, A]{
			NodeID:   ev.To.ID(),
			QueryID:  ev.QueryID,
			Response: shim.ClosestNodesFakeResponse(ev.Target, ev.ClosestNodes),
		}
	case *coord.EventGetClosestNodesFailure[K, A]:
		cmd = &query.EventPoolMessageFailure[K]{
			NodeID:  ev.To.ID(),
			QueryID: ev.QueryID,
			Error:   ev.Err,
		}
	default:
		panic(fmt.Sprintf("unexpected dht event: %T", ev))
	}

	// attempt to advance the query pool
	ev, ok := r.advancePool(ctx, cmd)
	if ok {
		r.pending = append(r.pending, ev)
	}
	if len(r.pending) > 0 {
		select {
		case r.ready <- struct{}{}:
		default:
		}
	}
}

func (r *PooledQueryBehaviour[K, A]) Ready() <-chan struct{} {
	return r.ready
}

func (r *PooledQueryBehaviour[K, A]) Perform(ctx context.Context) (coord.DhtEvent, bool) {
	ctx, span := util.StartSpan(ctx, "PooledQueryBehaviour.Perform")
	defer span.End()

	// No inbound work can be done until Perform is complete
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()

	for {
		// drain queued events first.
		if len(r.pending) > 0 {
			var ev coord.DhtEvent
			ev, r.pending = r.pending[0], r.pending[1:]

			if len(r.pending) > 0 {
				select {
				case r.ready <- struct{}{}:
				default:
				}
			}
			return ev, true
		}

		// attempt to advance the query pool
		ev, ok := r.advancePool(ctx, &query.EventPoolPoll{})
		if ok {
			return ev, true
		}

		if len(r.pending) == 0 {
			return nil, false
		}
	}
}

func (r *PooledQueryBehaviour[K, A]) advancePool(ctx context.Context, ev query.PoolEvent) (coord.DhtEvent, bool) {
	ctx, span := util.StartSpan(ctx, "PooledQueryBehaviour.advancePool")
	defer span.End()

	pstate := r.pool.Advance(ctx, ev)
	switch st := pstate.(type) {
	case *query.StatePoolQueryMessage[K, A]:
		return &coord.EventOutboundGetClosestNodes[K, A]{
			QueryID: st.QueryID,
			To:      shim.NewNodeAddr[K, A](st.NodeID, nil),
			Target:  st.Message.Target(),
			Notify:  r,
		}, true
	case *query.StatePoolWaitingAtCapacity:
		// nothing to do except wait for message response or timeout
	case *query.StatePoolWaitingWithCapacity:
		// nothing to do except wait for message response or timeout
	case *query.StatePoolQueryFinished:
		waiter, ok := r.waiters[st.QueryID]
		if ok {
			waiter.Notify(ctx, &coord.EventQueryFinished{
				QueryID: st.QueryID,
				Stats:   st.Stats,
			})
			waiter.Close()
		}
	case *query.StatePoolQueryTimeout:
		// TODO
	case *query.StatePoolIdle:
		// nothing to do
	default:
		panic(fmt.Sprintf("unexpected pool state: %T", st))
	}

	return nil, false
}
