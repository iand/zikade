package coord

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/benbjohnson/clock"
	"go.opentelemetry.io/otel/trace"

	"github.com/probe-lab/zikade/internal/coord/brdcst"
	"github.com/probe-lab/zikade/internal/coord/coordt"
	"github.com/probe-lab/zikade/kadt"
	"github.com/probe-lab/zikade/pb"
	"github.com/probe-lab/zikade/tele"
)

type PooledBroadcastBehaviour struct {
	logger *slog.Logger
	tracer trace.Tracer

	// clk supplies the instant each advance of the pool is applied at.
	clk clock.Clock

	// performMu is held while Perform is executing to ensure sequential execution of work.
	performMu sync.Mutex

	// pool is the broadcast pool state machine used for managing individual broadcasts.
	// it must only be accessed while performMu is held
	pool coordt.StateMachine[brdcst.PoolEvent, brdcst.PoolState]

	// pendingOutbound is a queue of outbound events.
	// it must only be accessed while performMu is held
	pendingOutbound []BehaviourEvent

	// notifiers is a map that keeps track of event notifications for each running broadcast.
	// it must only be accessed while performMu is held
	notifiers map[coordt.QueryID]*queryNotifier[*EventBroadcastFinished]

	// pendingInboundMu guards access to pendingInbound
	pendingInboundMu sync.Mutex

	// pendingInbound is a queue of inbound events that are awaiting processing
	pendingInbound []CtxEvent[BehaviourEvent]

	// nextDue is the time the broadcast pool last reported it could next make progress
	// without an event arriving, or the zero time if it reported none.
	// it must only be accessed while performMu is held
	nextDue time.Time

	// pollAgain records that the pool reported a broadcast ending rather than a due time,
	// so nextDue is stale until the pool is advanced again.
	// it must only be accessed while performMu is held
	pollAgain bool

	ready chan struct{}

	// readyTimer signals ready when the pool's next due time arrives.
	readyTimer *readyTimer
}

var _ Behaviour[BehaviourEvent, BehaviourEvent] = (*PooledBroadcastBehaviour)(nil)

func NewPooledBroadcastBehaviour(brdcstPool *brdcst.Pool[kadt.Key, kadt.PeerID, *pb.Message], clk clock.Clock, logger *slog.Logger, tracer trace.Tracer) *PooledBroadcastBehaviour {
	b := &PooledBroadcastBehaviour{
		pool:      brdcstPool,
		clk:       clk,
		notifiers: make(map[coordt.QueryID]*queryNotifier[*EventBroadcastFinished]),
		ready:     make(chan struct{}, 1),
		logger:    logger.With("behaviour", "pooledBroadcast"),
		tracer:    tracer,
	}
	b.readyTimer = newReadyTimer(clk, b.ready)
	return b
}

func (b *PooledBroadcastBehaviour) Ready() <-chan struct{} {
	return b.ready
}

func (b *PooledBroadcastBehaviour) Notify(ctx context.Context, ev BehaviourEvent) {
	b.pendingInboundMu.Lock()
	defer b.pendingInboundMu.Unlock()

	ctx, span := b.tracer.Start(ctx, "PooledBroadcastBehaviour.Notify")
	defer span.End()

	b.pendingInbound = append(b.pendingInbound, CtxEvent[BehaviourEvent]{Ctx: ctx, Event: ev})

	select {
	case b.ready <- struct{}{}:
	default:
	}
}

func (b *PooledBroadcastBehaviour) Perform(ctx context.Context) (out BehaviourEvent, performed bool) {
	b.performMu.Lock()
	defer b.performMu.Unlock()

	ctx, span := b.tracer.Start(ctx, "PooledBroadcastBehaviour.Perform")
	defer span.End()

	defer func() { b.updateReadyStatus(performed) }()

	// first send any pending query notifications
	for _, w := range b.notifiers {
		w.DrainPending()
	}

	// drain queued outbound events before starting new work.
	ev, ok := b.nextPendingOutbound()
	if ok {
		return ev, true
	}

	// perform one piece of pending inbound work.
	ev, ok = b.perfomNextInbound(ctx)
	if ok {
		return ev, true
	}

	// poll the broadcast pool to trigger any timeouts and other scheduled work
	ev, ok = b.advancePool(ctx, b.clk.Now(), &brdcst.EventPoolPoll{})
	if ok {
		return ev, true
	}

	// return any queued outbound work that may have been generated
	return b.nextPendingOutbound()
}

func (b *PooledBroadcastBehaviour) nextPendingOutbound() (BehaviourEvent, bool) {
	if len(b.pendingOutbound) == 0 {
		return nil, false
	}
	var ev BehaviourEvent
	ev, b.pendingOutbound = b.pendingOutbound[0], b.pendingOutbound[1:]
	return ev, true
}

func (b *PooledBroadcastBehaviour) nextPendingInbound() (CtxEvent[BehaviourEvent], bool) {
	b.pendingInboundMu.Lock()
	defer b.pendingInboundMu.Unlock()
	if len(b.pendingInbound) == 0 {
		return CtxEvent[BehaviourEvent]{}, false
	}
	var pev CtxEvent[BehaviourEvent]
	pev, b.pendingInbound = b.pendingInbound[0], b.pendingInbound[1:]
	return pev, true
}

// updateReadyStatus signals whether the behaviour has further work to do. It is
// called at the end of every Perform, passing whether that call produced an
// event.
//
// A Perform that produced an event may be able to produce another one straight
// away: the broadcast pool dispatches at most one message per advance, so a
// broadcast with several seed nodes needs several calls to contact them all.
// The event loop only calls Perform in response to a ready signal, so without
// re-signalling here a broadcast would contact one node and then wait for that
// node's response before contacting the next.
//
// A behaviour with no work to do arms a timer for the broadcast's next due time.
func (b *PooledBroadcastBehaviour) updateReadyStatus(performed bool) {
	if performed || b.pollAgain || len(b.pendingOutbound) != 0 {
		signalReady(b.ready)
		return
	}

	b.pendingInboundMu.Lock()
	hasPendingInbound := len(b.pendingInbound) != 0
	b.pendingInboundMu.Unlock()

	if hasPendingInbound {
		signalReady(b.ready)
		return
	}

	b.readyTimer.Arm(b.nextDue)
}

func (b *PooledBroadcastBehaviour) perfomNextInbound(ctx context.Context) (BehaviourEvent, bool) {
	ctx, span := b.tracer.Start(ctx, "PooledBroadcastBehaviour.perfomNextInbound")
	defer span.End()
	pev, ok := b.nextPendingInbound()
	if !ok {
		return nil, false
	}

	var cmd brdcst.PoolEvent
	switch ev := pev.Event.(type) {
	case *EventStartBroadcast:
		cmd = &brdcst.EventPoolStartBroadcast[kadt.Key, kadt.PeerID, *pb.Message]{
			QueryID: ev.QueryID,
			Target:  ev.Target,
			Message: ev.Message,
			Seed:    ev.Seed,
			Config:  ev.Config,
		}
		if ev.Notify != nil {
			b.notifiers[ev.QueryID] = &queryNotifier[*EventBroadcastFinished]{monitor: ev.Notify}
		}

	case *EventGetCloserNodesSuccess:
		for _, info := range ev.CloserNodes {
			b.pendingOutbound = append(b.pendingOutbound, &EventAddNode{
				NodeID: info,
			})
		}

		waiter, ok := b.notifiers[ev.QueryID]
		if ok {
			waiter.TryNotifyProgressed(ctx, &EventQueryProgressed{
				NodeID:  ev.To,
				QueryID: ev.QueryID,
			})
		}

		cmd = &brdcst.EventPoolGetCloserNodesSuccess[kadt.Key, kadt.PeerID]{
			NodeID:      ev.To,
			QueryID:     ev.QueryID,
			Target:      ev.Target,
			CloserNodes: ev.CloserNodes,
		}

	case *EventGetCloserNodesFailure:
		// queue an event that will notify the routing behaviour of a failed node
		b.pendingOutbound = append(b.pendingOutbound, &EventNotifyNonConnectivity{
			ev.To,
		})

		cmd = &brdcst.EventPoolGetCloserNodesFailure[kadt.Key, kadt.PeerID]{
			NodeID:  ev.To,
			QueryID: ev.QueryID,
			Target:  ev.Target,
			Error:   ev.Err,
		}

	case *EventSendMessageSuccess:
		for _, info := range ev.CloserNodes {
			b.pendingOutbound = append(b.pendingOutbound, &EventAddNode{
				NodeID: info,
			})
		}
		waiter, ok := b.notifiers[ev.QueryID]
		if ok {
			waiter.TryNotifyProgressed(ctx, &EventQueryProgressed{
				NodeID:   ev.To,
				QueryID:  ev.QueryID,
				Response: ev.Response,
			})
		}
		if err := verifyStoredRecord(ev.Request, ev.Response); err != nil {
			cmd = &brdcst.EventPoolStoreRecordFailure[kadt.Key, kadt.PeerID, *pb.Message]{
				QueryID: ev.QueryID,
				NodeID:  ev.To,
				Request: ev.Request,
				Error:   err,
			}
			break
		}

		// TODO: How do we know it's a StoreRecord response?
		cmd = &brdcst.EventPoolStoreRecordSuccess[kadt.Key, kadt.PeerID, *pb.Message]{
			QueryID:  ev.QueryID,
			NodeID:   ev.To,
			Request:  ev.Request,
			Response: ev.Response,
		}

	case *EventSendMessageFailure:
		// queue an event that will notify the routing behaviour of a failed node
		b.pendingOutbound = append(b.pendingOutbound, &EventNotifyNonConnectivity{
			ev.To,
		})

		// TODO: How do we know it's a StoreRecord response?
		cmd = &brdcst.EventPoolStoreRecordFailure[kadt.Key, kadt.PeerID, *pb.Message]{
			NodeID:  ev.To,
			QueryID: ev.QueryID,
			Request: ev.Request,
			Error:   ev.Err,
		}

	case *EventStopQuery:
		cmd = &brdcst.EventPoolStopBroadcast{
			QueryID: ev.QueryID,
		}
	}

	// attempt to advance the broadcast pool
	return b.advancePool(ctx, b.clk.Now(), cmd)
}

func (b *PooledBroadcastBehaviour) advancePool(ctx context.Context, now time.Time, ev brdcst.PoolEvent) (out BehaviourEvent, term bool) {
	ctx, span := b.tracer.Start(ctx, "PooledBroadcastBehaviour.advancePool", trace.WithAttributes(tele.AttrInEvent(ev)))
	defer func() {
		span.SetAttributes(tele.AttrOutEvent(out))
		span.End()
	}()

	b.pollAgain = false

	pstate := b.pool.Advance(ctx, now, ev)
	switch st := pstate.(type) {
	case *brdcst.StatePoolIdle:
		// nothing to do
		b.nextDue = time.Time{}
	case *brdcst.StatePoolWaiting:
		// nothing to do except wait for message responses or timeouts
		b.nextDue = st.NextDue
	case *brdcst.StatePoolFindCloser[kadt.Key, kadt.PeerID]:
		return &EventOutboundGetCloserNodes{
			QueryID: st.QueryID,
			To:      st.NodeID,
			Target:  st.Target,
			Notify:  b,
		}, true
	case *brdcst.StatePoolStoreRecord[kadt.Key, kadt.PeerID, *pb.Message]:
		return &EventOutboundSendMessage{
			QueryID: st.QueryID,
			To:      st.NodeID,
			Message: st.Message,
			Notify:  b,
		}, true
	case *brdcst.StatePoolBroadcastFinished[kadt.Key, kadt.PeerID]:
		// the state carries no due time and the pool has removed the broadcast, so the
		// pool must be advanced again to report when the remaining broadcasts are next due
		b.pollAgain = true
		waiter, ok := b.notifiers[st.QueryID]
		if ok {
			waiter.NotifyFinished(ctx, &EventBroadcastFinished{
				QueryID:   st.QueryID,
				Contacted: st.Contacted,
				Errors:    st.Errors,
			})
			delete(b.notifiers, st.QueryID)
		}
	}

	return nil, false
}

// A BroadcastWaiter implements [QueryMonitor] for broadcasts
type BroadcastWaiter struct {
	progressed chan CtxEvent[*EventQueryProgressed]
	finished   chan CtxEvent[*EventBroadcastFinished]
}

var _ QueryMonitor[*EventBroadcastFinished] = (*BroadcastWaiter)(nil)

func NewBroadcastWaiter(n int) *BroadcastWaiter {
	w := &BroadcastWaiter{
		progressed: make(chan CtxEvent[*EventQueryProgressed], n),
		finished:   make(chan CtxEvent[*EventBroadcastFinished], 1),
	}
	return w
}

func (w *BroadcastWaiter) Progressed() <-chan CtxEvent[*EventQueryProgressed] {
	return w.progressed
}

func (w *BroadcastWaiter) Finished() <-chan CtxEvent[*EventBroadcastFinished] {
	return w.finished
}

func (w *BroadcastWaiter) NotifyProgressed() chan<- CtxEvent[*EventQueryProgressed] {
	return w.progressed
}

func (w *BroadcastWaiter) NotifyFinished() chan<- CtxEvent[*EventBroadcastFinished] {
	return w.finished
}

// verifyStoredRecord checks that a remote node stored the record it was sent.
// Only PUT_VALUE draws an echoed record back.
func verifyStoredRecord(req, resp *pb.Message) error {
	if req.GetType() != pb.Message_PUT_VALUE {
		return nil
	}

	if resp == nil {
		return fmt.Errorf("no response to PUT_VALUE")
	}

	if !bytes.Equal(resp.GetRecord().GetValue(), req.GetRecord().GetValue()) {
		return fmt.Errorf("record not stored correctly")
	}

	return nil
}
