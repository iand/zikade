package coord

import (
	"context"
	"testing"
	"time"

	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/require"

	"github.com/probe-lab/zikade/internal/coord/brdcst"
	"github.com/probe-lab/zikade/internal/coord/cplutil"
	"github.com/probe-lab/zikade/internal/coord/query"
	"github.com/probe-lab/zikade/internal/coord/routing"
	"github.com/probe-lab/zikade/internal/kadtest"
	"github.com/probe-lab/zikade/internal/nettest"
	"github.com/probe-lab/zikade/kadt"
	"github.com/probe-lab/zikade/pb"
	"github.com/probe-lab/zikade/tele"
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

// testPeers returns n peers wired into a linear topology.
func testPeers(t *testing.T, n int) []*nettest.Peer {
	t.Helper()
	_, nodes, err := nettest.LinearTopology(n, clock.New())
	require.NoError(t, err)
	return nodes
}

// newTestQueryBehaviour returns a query behaviour that gives up on a query after
// timeout and on an individual request after requestTimeout.
func newTestQueryBehaviour(t *testing.T, clk clock.Clock, timeout, requestTimeout time.Duration, self kadt.PeerID) *QueryBehaviour {
	t.Helper()

	cfg := DefaultQueryConfig()
	cfg.Clock = clk
	cfg.Timeout = timeout
	cfg.RequestTimeout = requestTimeout

	b, err := NewQueryBehaviour(self, cfg)
	require.NoError(t, err)
	return b
}

// newTestBroadcastBehaviour returns a broadcast behaviour holding an empty pool.
func newTestBroadcastBehaviour(t *testing.T, clk clock.Clock, self kadt.PeerID) *PooledBroadcastBehaviour {
	t.Helper()

	pool, err := brdcst.NewPool[kadt.Key, kadt.PeerID, *pb.Message](self, nil)
	require.NoError(t, err)

	return NewPooledBroadcastBehaviour(pool, clk, tele.DefaultLogger("coord"), tele.NoopTracer())
}

// buildWaitingQueryBehaviour returns a builder for a query behaviour running a query
// that has contacted a node and is waiting for a response that never arrives.
func buildWaitingQueryBehaviour(timeout, requestTimeout time.Duration) func(t *testing.T, ctx context.Context, clk clock.Clock) Behaviour[BehaviourEvent, BehaviourEvent] {
	return func(t *testing.T, ctx context.Context, clk clock.Clock) Behaviour[BehaviourEvent, BehaviourEvent] {
		nodes := testPeers(t, 2)

		b := newTestQueryBehaviour(t, clk, timeout, requestTimeout, nodes[0].NodeID)
		b.Notify(ctx, &EventStartFindCloserQuery{
			QueryID:           "test",
			Target:            nodes[1].NodeID.Key(),
			KnownClosestNodes: []kadt.PeerID{nodes[1].NodeID},
		})

		return b
	}
}

// buildWaitingBootstrapBehaviour returns a routing behaviour whose bootstrap has
// contacted a seed and is waiting for a response that never arrives.
func buildWaitingBootstrapBehaviour(t *testing.T, ctx context.Context, clk clock.Clock) Behaviour[BehaviourEvent, BehaviourEvent] {
	nodes := testPeers(t, 2)

	bcfg := routing.DefaultBootstrapConfig()
	bcfg.Timeout = 10 * conformanceDeadline
	bcfg.RequestTimeout = conformanceDeadline

	bootstrap, err := routing.NewBootstrap[kadt.Key](nodes[0].NodeID, bcfg)
	require.NoError(t, err)

	cfg := DefaultRoutingConfig()
	cfg.Clock = clk

	b, err := ComposeRoutingBehaviour(nodes[0].NodeID, bootstrap, idleInclude(), idleProbe(), idleExplore(), cfg)
	require.NoError(t, err)

	b.Notify(ctx, &EventStartBootstrap{
		SeedNodes: []kadt.PeerID{nodes[1].NodeID},
	})

	return b
}

// buildWaitingIncludeBehaviour returns a routing behaviour whose include has started a
// connectivity check for a candidate node that never answers.
func buildWaitingIncludeBehaviour(t *testing.T, ctx context.Context, clk clock.Clock) Behaviour[BehaviourEvent, BehaviourEvent] {
	nodes := testPeers(t, 4)

	icfg := routing.DefaultIncludeConfig()
	icfg.Timeout = conformanceDeadline

	include, err := routing.NewInclude[kadt.Key, kadt.PeerID](nodes[0].RoutingTable, icfg)
	require.NoError(t, err)

	cfg := DefaultRoutingConfig()
	cfg.Clock = clk

	b, err := ComposeRoutingBehaviour(nodes[0].NodeID, idleBootstrap(), include, idleProbe(), idleExplore(), cfg)
	require.NoError(t, err)

	b.Notify(ctx, &EventAddNode{
		NodeID: nodes[len(nodes)-1].NodeID,
	})

	return b
}

// buildWaitingProbeBehaviour returns a routing behaviour whose probe holds a node whose
// next connectivity check is due after the check interval.
func buildWaitingProbeBehaviour(t *testing.T, ctx context.Context, clk clock.Clock) Behaviour[BehaviourEvent, BehaviourEvent] {
	nodes := testPeers(t, 2)

	pcfg := routing.DefaultProbeConfig()
	pcfg.CheckInterval = conformanceDeadline

	probe, err := routing.NewProbe[kadt.Key](nodes[0].RoutingTable, pcfg)
	require.NoError(t, err)

	cfg := DefaultRoutingConfig()
	cfg.Clock = clk

	b, err := ComposeRoutingBehaviour(nodes[0].NodeID, idleBootstrap(), idleInclude(), probe, idleExplore(), cfg)
	require.NoError(t, err)

	// the linear topology puts the second node in the first node's routing table, which
	// is where the probe takes the nodes it checks from
	b.Notify(ctx, &EventRoutingUpdated{
		NodeID: nodes[1].NodeID,
	})

	return b
}

// buildWaitingExploreBehaviour returns a routing behaviour whose explore has nothing to
// do until the first cpl in its schedule falls due.
func buildWaitingExploreBehaviour(t *testing.T, ctx context.Context, clk clock.Clock) Behaviour[BehaviourEvent, BehaviourEvent] {
	nodes := testPeers(t, 2)

	cfg := DefaultRoutingConfig()
	cfg.Clock = clk

	schedule, err := routing.NewDynamicExploreSchedule(cfg.ExploreMaximumCpl, clk.Now(), conformanceDeadline, cfg.ExploreIntervalMultiplier, cfg.ExploreIntervalJitter)
	require.NoError(t, err)

	explore, err := routing.NewExplore[kadt.Key](nodes[0].NodeID, nodes[0].RoutingTable, cplutil.GenRandPeerID, schedule, routing.DefaultExploreConfig())
	require.NoError(t, err)

	b, err := ComposeRoutingBehaviour(nodes[0].NodeID, idleBootstrap(), idleInclude(), idleProbe(), explore, cfg)
	require.NoError(t, err)

	return b
}

// buildWaitingBroadcastBehaviour returns a broadcast behaviour running a follow up
// broadcast that has contacted a seed and is waiting for a response that never arrives.
func buildWaitingBroadcastBehaviour(t *testing.T, ctx context.Context, clk clock.Clock) Behaviour[BehaviourEvent, BehaviourEvent] {
	nodes := testPeers(t, 2)

	// the pool the broadcast borrows takes no configuration, so this case depends on its
	// default request timeout matching the deadline the other cases use
	require.Equal(t, conformanceDeadline, query.DefaultPoolConfig().RequestTimeout)

	b := newTestBroadcastBehaviour(t, clk, nodes[0].NodeID)

	msg := &pb.Message{Type: pb.Message_PUT_VALUE}
	b.Notify(ctx, &EventStartBroadcast{
		QueryID: "test",
		Target:  msg.Target(),
		Message: msg,
		Seed:    []kadt.PeerID{nodes[1].NodeID},
		Config:  brdcst.DefaultConfigFollowUp(),
	})

	return b
}

// conformanceDeadline is the deadline every behaviour built by
// [TestBehaviourSignalsReadyAtDeadline] is waiting on, measured from its first advance.
const conformanceDeadline = time.Minute

// TestBehaviourSignalsReadyAtDeadline checks the [Behaviour] ready contract for every
// behaviour that owns a deadline: one waiting on a deadline signals ready by the time it
// falls due. Each case leaves its behaviour waiting on a response that never comes, and
// the events it emits are dropped, so only the passage of time can make work available.
func TestBehaviourSignalsReadyAtDeadline(t *testing.T) {
	testCases := []struct {
		name string
		// build returns a behaviour that will be waiting on a deadline of
		// conformanceDeadline once it has been driven until it has no more work.
		build func(t *testing.T, ctx context.Context, clk clock.Clock) Behaviour[BehaviourEvent, BehaviourEvent]
	}{
		{
			name:  "query request timeout",
			build: buildWaitingQueryBehaviour(10*conformanceDeadline, conformanceDeadline),
		},
		{
			name:  "query timeout",
			build: buildWaitingQueryBehaviour(conformanceDeadline, 10*conformanceDeadline),
		},
		{
			name:  "bootstrap request timeout",
			build: buildWaitingBootstrapBehaviour,
		},
		{
			name:  "include connectivity check timeout",
			build: buildWaitingIncludeBehaviour,
		},
		{
			name:  "probe check interval",
			build: buildWaitingProbeBehaviour,
		},
		{
			name:  "explore schedule",
			build: buildWaitingExploreBehaviour,
		},
		{
			name:  "broadcast request timeout",
			build: buildWaitingBroadcastBehaviour,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := kadtest.CtxShort(t)
			clk := clock.NewMock()

			b := tc.build(t, ctx, clk)

			// drive the behaviour until it has no more work, discarding generated events
			PerformWhileReady(t, ctx, b)

			select {
			case <-b.Ready():
				t.Fatal("behaviour signalled ready before its deadline fell due")
			default:
			}

			clk.Add(conformanceDeadline)

			select {
			case <-b.Ready():
			case <-time.After(time.Second):
				t.Fatalf("behaviour did not signal ready by its deadline of %s", conformanceDeadline)
			}
		})
	}
}

// TestBehaviourWithNoWorkArmsNoTimer checks that a behaviour holding no deadline holds
// no timer either, so an idle node goes quiet instead of waking on a schedule.
func TestBehaviourWithNoWorkArmsNoTimer(t *testing.T) {
	testCases := []struct {
		name  string
		build func(t *testing.T, clk clock.Clock) (Behaviour[BehaviourEvent, BehaviourEvent], *readyTimer)
	}{
		{
			name: "query",
			build: func(t *testing.T, clk clock.Clock) (Behaviour[BehaviourEvent, BehaviourEvent], *readyTimer) {
				b := newTestQueryBehaviour(t, clk, time.Minute, time.Minute, testPeers(t, 1)[0].NodeID)
				return b, b.readyTimer
			},
		},
		{
			name: "routing",
			build: func(t *testing.T, clk clock.Clock) (Behaviour[BehaviourEvent, BehaviourEvent], *readyTimer) {
				cfg := DefaultRoutingConfig()
				cfg.Clock = clk

				b, err := ComposeRoutingBehaviour(testPeers(t, 1)[0].NodeID, idleBootstrap(), idleInclude(), idleProbe(), idleExplore(), cfg)
				require.NoError(t, err)
				return b, b.readyTimer
			},
		},
		{
			name: "broadcast",
			build: func(t *testing.T, clk clock.Clock) (Behaviour[BehaviourEvent, BehaviourEvent], *readyTimer) {
				b := newTestBroadcastBehaviour(t, clk, testPeers(t, 1)[0].NodeID)
				return b, b.readyTimer
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := kadtest.CtxShort(t)
			clk := clock.NewMock()

			b, timer := tc.build(t, clk)

			PerformWhileReady(t, ctx, b)

			if timer.timer != nil {
				t.Error("behaviour armed a timer with no work outstanding")
			}

			clk.Add(24 * time.Hour)

			select {
			case <-b.Ready():
				t.Error("behaviour signalled ready with no work outstanding")
			default:
			}
		})
	}
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
