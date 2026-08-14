package coord

import (
	"context"
	"sync"
	"testing"

	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/require"

	"github.com/probe-lab/zikade/internal/kadtest"
	"github.com/probe-lab/zikade/internal/nettest"
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
