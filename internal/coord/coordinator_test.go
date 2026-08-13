package coord

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/require"

	"github.com/probe-lab/zikade/internal/coord/coordt"
	"github.com/probe-lab/zikade/internal/kadtest"
	"github.com/probe-lab/zikade/internal/nettest"
	"github.com/probe-lab/zikade/kadt"
	"github.com/probe-lab/zikade/pb"
)

func TestConfigValidate(t *testing.T) {
	t.Run("default is valid", func(t *testing.T) {
		cfg := DefaultCoordinatorConfig()

		require.NoError(t, cfg.Validate())
	})

	t.Run("clock is not nil", func(t *testing.T) {
		cfg := DefaultCoordinatorConfig()

		cfg.Clock = nil
		require.Error(t, cfg.Validate())
	})

	t.Run("logger not nil", func(t *testing.T) {
		cfg := DefaultCoordinatorConfig()

		cfg.Logger = nil
		require.Error(t, cfg.Validate())
	})

	t.Run("meter provider not nil", func(t *testing.T) {
		cfg := DefaultCoordinatorConfig()
		cfg.MeterProvider = nil
		require.Error(t, cfg.Validate())
	})

	t.Run("tracer provider not nil", func(t *testing.T) {
		cfg := DefaultCoordinatorConfig()
		cfg.TracerProvider = nil
		require.Error(t, cfg.Validate())
	})
}

func TestExhaustiveQuery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := kadtest.CtxBubble(t)

		_, nodes, err := nettest.LinearTopology(4, clock.New())
		require.NoError(t, err)

		ccfg := DefaultCoordinatorConfig()

		// A (ids[0]) is looking for D (ids[3])
		// A will first ask B, B will reply with C's address (and A's address)
		// A will then ask C, C will reply with D's address (and B's address)
		self := nodes[0].NodeID
		c, err := NewCoordinator(self, nodes[0].Router, nodes[0].RoutingTable, ccfg)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, c.Close()) })

		target := nodes[3].NodeID.Key()

		visited := make(map[string]int)

		// Record the nodes as they are visited
		qfn := func(ctx context.Context, id kadt.PeerID, msg *pb.Message, stats coordt.QueryStats) error {
			visited[id.String()]++
			return nil
		}

		// Run a query to find the value
		_, _, err = c.QueryClosest(ctx, target, qfn, 20)
		require.NoError(t, err)

		require.Equal(t, 3, len(visited))
		require.Contains(t, visited, nodes[1].NodeID.String())
		require.Contains(t, visited, nodes[2].NodeID.String())
		require.Contains(t, visited, nodes[3].NodeID.String())
	})
}

// TestQueryReturnsClosestNodes checks that a query which runs to exhaustion hands its
// caller the closest nodes it found. The notifier closes the progress channel before it
// sends the terminal event, so a caller selecting on both can be woken by the close and
// return before the terminal event arrives.
func TestQueryReturnsClosestNodes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := kadtest.CtxBubble(t)

		_, nodes, err := nettest.LinearTopology(4, clock.New())
		require.NoError(t, err)

		self := nodes[0].NodeID
		c, err := NewCoordinator(self, nodes[0].Router, nodes[0].RoutingTable, DefaultCoordinatorConfig())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, c.Close()) })

		qfn := func(ctx context.Context, id kadt.PeerID, msg *pb.Message, stats coordt.QueryStats) error {
			return nil
		}

		closest, stats, err := c.QueryClosest(ctx, nodes[3].NodeID.Key(), qfn, 20)
		require.NoError(t, err)
		require.True(t, stats.Exhausted)
		require.NotEmpty(t, closest)
	})
}

func TestRoutingUpdatedEventEmittedForCloserNodes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := kadtest.CtxBubble(t)

		_, nodes, err := nettest.LinearTopology(4, clock.New())
		require.NoError(t, err)

		ccfg := DefaultCoordinatorConfig()

		// A (ids[0]) is looking for D (ids[3])
		// A will first ask B, B will reply with C's address (and A's address)
		// A will then ask C, C will reply with D's address (and B's address)
		self := nodes[0].NodeID
		c, err := NewCoordinator(self, nodes[0].Router, nodes[0].RoutingTable, ccfg)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, c.Close()) })

		rn := NewBufferedRoutingNotifier()
		c.SetRoutingNotifier(rn)

		qfn := func(ctx context.Context, id kadt.PeerID, msg *pb.Message, stats coordt.QueryStats) error {
			return nil
		}

		// Run a query to find the value
		target := nodes[3].NodeID.Key()
		_, _, err = c.QueryClosest(ctx, target, qfn, 20)
		require.NoError(t, err)

		// the query run by the dht should have received a response from nodes[1] with closer nodes
		// nodes[0] and nodes[2] which should trigger a routing table update since nodes[2] was
		// not in the dht's routing table.
		// the query then continues and should have received a response from nodes[2] with closer nodes
		// nodes[1] and nodes[3] which should trigger a routing table update since nodes[3] was
		// not in the dht's routing table.

		// no EventRoutingUpdated is sent for the self node

		// wait for the coordinator to finish dispatching the events emitted by the query
		synctest.Wait()

		// the order in which these events are emitted may vary depending on the
		// order in which the coordinator drained its behaviours
		ev1, err := rn.Expect(ctx, &EventRoutingUpdated{})
		require.NoError(t, err)
		tev1 := ev1.(*EventRoutingUpdated)

		ev2, err := rn.Expect(ctx, &EventRoutingUpdated{})
		require.NoError(t, err)
		tev2 := ev2.(*EventRoutingUpdated)

		if tev1.NodeID.Equal(nodes[2].NodeID) {
			require.Equal(t, nodes[3].NodeID, tev2.NodeID)
		} else if tev2.NodeID.Equal(nodes[2].NodeID) {
			require.Equal(t, nodes[3].NodeID, tev1.NodeID)
		} else {
			require.Failf(t, "did not see routing updated event for %s", nodes[2].NodeID.String())
		}
	})
}

func TestBootstrap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := kadtest.CtxBubble(t)

		_, nodes, err := nettest.LinearTopology(4, clock.New())
		require.NoError(t, err)

		ccfg := DefaultCoordinatorConfig()

		self := nodes[0].NodeID
		d, err := NewCoordinator(self, nodes[0].Router, nodes[0].RoutingTable, ccfg)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, d.Close()) })

		rn := NewBufferedRoutingNotifier()
		d.SetRoutingNotifier(rn)

		seeds := []kadt.PeerID{nodes[1].NodeID}
		err = d.Bootstrap(ctx, seeds)
		require.NoError(t, err)

		// bootstrap runs in the background, wait for the coordinator to settle
		synctest.Wait()

		// the query run by the dht should have completed
		ev, err := rn.Expect(ctx, &EventBootstrapFinished{})
		require.NoError(t, err)

		require.IsType(t, &EventBootstrapFinished{}, ev)
		tevf := ev.(*EventBootstrapFinished)
		require.Equal(t, 3, tevf.Stats.Requests)
		require.Equal(t, 3, tevf.Stats.Success)
		require.Equal(t, 0, tevf.Stats.Failure)

		_, err = rn.Expect(ctx, &EventRoutingUpdated{})
		require.NoError(t, err)

		_, err = rn.Expect(ctx, &EventRoutingUpdated{})
		require.NoError(t, err)

		// coordinator will have node1 in its routing table
		require.True(t, d.IsRoutable(ctx, nodes[1].NodeID))

		// coordinator should now have node2 in its routing table
		require.True(t, d.IsRoutable(ctx, nodes[2].NodeID))

		// coordinator should now have node3 in its routing table
		require.True(t, d.IsRoutable(ctx, nodes[3].NodeID))
	})
}

func TestIncludeNode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := kadtest.CtxBubble(t)

		_, nodes, err := nettest.LinearTopology(4, clock.New())
		require.NoError(t, err)

		ccfg := DefaultCoordinatorConfig()

		candidate := nodes[len(nodes)-1].NodeID // not in nodes[0] routing table

		self := nodes[0].NodeID
		d, err := NewCoordinator(self, nodes[0].Router, nodes[0].RoutingTable, ccfg)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, d.Close()) })

		rn := NewBufferedRoutingNotifier()
		d.SetRoutingNotifier(rn)

		// the routing table should not contain the node yet
		require.False(t, d.IsRoutable(ctx, candidate))

		// inject a new node
		err = d.AddNodes(ctx, []kadt.PeerID{candidate})
		require.NoError(t, err)

		// the include state machine runs in the background, wait for it to finish
		synctest.Wait()

		ev, err := rn.Expect(ctx, &EventRoutingUpdated{})
		require.NoError(t, err)

		tev := ev.(*EventRoutingUpdated)
		require.Equal(t, candidate, tev.NodeID)

		// the routing table should now contain the node
		require.True(t, d.IsRoutable(ctx, candidate))
	})
}
