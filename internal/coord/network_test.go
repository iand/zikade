package coord

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/require"

	"github.com/probe-lab/zikade/internal/coord/coordt"
	"github.com/probe-lab/zikade/internal/kadtest"
	"github.com/probe-lab/zikade/internal/nettest"
	"github.com/probe-lab/zikade/kadt"
	"github.com/probe-lab/zikade/pb"
)

// TestNetworkBehaviourDropsRequestsBeyondPeerCapacity checks that requests to a peer that
// has used up its capacity are refused and reported back as failures, rather than made to
// wait for the peer, which would stop the event loop that is the only thing able to drain
// the handler.
func TestNetworkBehaviourDropsRequestsBeyondPeerCapacity(t *testing.T) {
	ctx := kadtest.CtxShort(t)

	_, nodes, err := nettest.LinearTopology(2, clock.New())
	require.NoError(t, err)

	cfg := DefaultNetworkConfig()
	cfg.PeerCapacity = 1

	// the router never answers, so the first request holds the peer's only slot for the
	// rest of the test
	b, err := NewNetworkBehaviour(&silentRouter{}, cfg)
	require.NoError(t, err)
	t.Cleanup(b.Close)

	var mu sync.Mutex
	var failures []*EventGetCloserNodesFailure

	notify := NotifyFunc[BehaviourEvent](func(ctx context.Context, ev BehaviourEvent) {
		mu.Lock()
		defer mu.Unlock()
		if fev, ok := ev.(*EventGetCloserNodesFailure); ok {
			failures = append(failures, fev)
		}
	})

	for range 3 {
		b.Notify(ctx, &EventOutboundGetCloserNodes{
			QueryID: "test",
			To:      nodes[1].NodeID,
			Target:  nodes[1].NodeID.Key(),
			Notify:  notify,
		})
	}

	mu.Lock()
	defer mu.Unlock()

	require.Len(t, failures, 2)
	for _, fev := range failures {
		require.ErrorIs(t, fev.Err, ErrRequestDropped)
		require.Equal(t, nodes[1].NodeID, fev.To)
	}
}

// promptRouter answers every request immediately.
type promptRouter struct{}

var _ coordt.Router[kadt.Key, kadt.PeerID, *pb.Message] = (*promptRouter)(nil)

func (r *promptRouter) SendMessage(ctx context.Context, to kadt.PeerID, req *pb.Message) (*pb.Message, error) {
	return &pb.Message{}, nil
}

func (r *promptRouter) GetClosestNodes(ctx context.Context, to kadt.PeerID, target kadt.Key) ([]kadt.PeerID, error) {
	return nil, nil
}

// handlerCount returns the number of node handlers the behaviour is holding.
func (b *NetworkBehaviour) handlerCount() int {
	b.nodeHandlersMu.Lock()
	defer b.nodeHandlersMu.Unlock()
	return len(b.nodeHandlers)
}

// handlerFor returns the node handler the behaviour holds for a peer, or nil if it holds none.
func (b *NetworkBehaviour) handlerFor(to kadt.PeerID) *NodeHandler {
	b.nodeHandlersMu.Lock()
	defer b.nodeHandlersMu.Unlock()
	return b.nodeHandlers[to]
}

// TestNetworkBehaviourEvictsIdleNodeHandlers checks that an unused node handler is removed
// once it has been idle for the configured timeout, and that its goroutine exits.
func TestNetworkBehaviourEvictsIdleNodeHandlers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := kadtest.CtxBubble(t)

		_, nodes, err := nettest.LinearTopology(4, clock.New())
		require.NoError(t, err)

		cfg := DefaultNetworkConfig()

		b, err := NewNetworkBehaviour(&promptRouter{}, cfg)
		require.NoError(t, err)
		t.Cleanup(b.Close)

		notify := NotifyFunc[BehaviourEvent](func(ctx context.Context, ev BehaviourEvent) {})

		peers := nodes[1:]
		for _, n := range peers {
			b.Notify(ctx, &EventOutboundGetCloserNodes{
				QueryID: "test",
				To:      n.NodeID,
				Target:  n.NodeID.Key(),
				Notify:  notify,
			})
		}

		synctest.Wait()
		require.Equal(t, len(peers), b.handlerCount())

		// hold one handler so its goroutine can be checked after it has been evicted
		nh := b.handlerFor(peers[0].NodeID)
		require.NotNil(t, nh)

		time.Sleep(cfg.IdleTimeout + time.Second)
		synctest.Wait()

		require.Zero(t, b.handlerCount())

		select {
		case <-nh.done:
		default:
			t.Fatal("node handler goroutine did not exit")
		}
	})
}

// blockingRouter holds every request until release is closed.
type blockingRouter struct {
	release chan struct{}
}

var _ coordt.Router[kadt.Key, kadt.PeerID, *pb.Message] = (*blockingRouter)(nil)

func (r *blockingRouter) SendMessage(ctx context.Context, to kadt.PeerID, req *pb.Message) (*pb.Message, error) {
	<-r.release
	return &pb.Message{}, nil
}

func (r *blockingRouter) GetClosestNodes(ctx context.Context, to kadt.PeerID, target kadt.Key) ([]kadt.PeerID, error) {
	<-r.release
	return nil, nil
}

// TestNetworkBehaviourKeepsBusyNodeHandlers checks that a node handler with a request still
// in flight survives its idle timeout, and is evicted only once that request has completed.
func TestNetworkBehaviourKeepsBusyNodeHandlers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := kadtest.CtxBubble(t)

		_, nodes, err := nettest.LinearTopology(2, clock.New())
		require.NoError(t, err)

		cfg := DefaultNetworkConfig()

		rtr := &blockingRouter{release: make(chan struct{})}
		b, err := NewNetworkBehaviour(rtr, cfg)
		require.NoError(t, err)
		t.Cleanup(b.Close)

		notify := NotifyFunc[BehaviourEvent](func(ctx context.Context, ev BehaviourEvent) {})

		b.Notify(ctx, &EventOutboundGetCloserNodes{
			QueryID: "test",
			To:      nodes[1].NodeID,
			Target:  nodes[1].NodeID.Key(),
			Notify:  notify,
		})

		synctest.Wait()
		require.Equal(t, 1, b.handlerCount())

		time.Sleep(cfg.IdleTimeout + time.Second)
		synctest.Wait()
		require.Equal(t, 1, b.handlerCount(), "handler with a request in flight was evicted")

		close(rtr.release)
		synctest.Wait()

		time.Sleep(cfg.IdleTimeout + time.Second)
		synctest.Wait()
		require.Zero(t, b.handlerCount(), "handler was not evicted after its request completed")
	})
}

// TestEvictIdleRefusesBusyHandler checks that evictIdle declines a handler holding a
// request, the case that arises when one is accepted between the idle timer firing and the
// eviction taking the handler map lock.
func TestEvictIdleRefusesBusyHandler(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := kadtest.CtxBubble(t)

		_, nodes, err := nettest.LinearTopology(2, clock.New())
		require.NoError(t, err)

		rtr := &blockingRouter{release: make(chan struct{})}
		b, err := NewNetworkBehaviour(rtr, DefaultNetworkConfig())
		require.NoError(t, err)
		t.Cleanup(b.Close)

		notify := NotifyFunc[BehaviourEvent](func(ctx context.Context, ev BehaviourEvent) {})

		b.Notify(ctx, &EventOutboundGetCloserNodes{
			QueryID: "test",
			To:      nodes[1].NodeID,
			Target:  nodes[1].NodeID.Key(),
			Notify:  notify,
		})
		synctest.Wait()

		nh := b.handlerFor(nodes[1].NodeID)
		require.NotNil(t, nh)

		require.False(t, b.evictIdle(nh), "a handler with a request outstanding was evicted")
		require.Equal(t, 1, b.handlerCount())

		close(rtr.release)
		synctest.Wait()

		require.True(t, b.evictIdle(nh), "a handler with nothing outstanding was not evicted")
		require.Zero(t, b.handlerCount())
	})
}
