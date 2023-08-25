package coord

// TODO: port these tests to dht package

// import (
// 	"context"
// 	"fmt"
// 	"log"
// 	"reflect"
// 	"testing"
// 	"time"

// 	"github.com/benbjohnson/clock"
// 	"github.com/stretchr/testify/require"

// 	"github.com/plprobelab/go-kademlia/kad"
// 	"github.com/plprobelab/go-kademlia/key"

// 	"github.com/iand/zikade/core"
// 	"github.com/iand/zikade/internal/kadtest"
// 	"github.com/iand/zikade/internal/nettest"
// )

// var (
// 	_ core.DHT[key.Key8, kadtest.StrAddr] = (*Dht[key.Key8, kadtest.StrAddr])(nil)
// 	// _ core.FindNodeDHT[key.Key8, kadtest.StrAddr] = (*Dht[key.Key8, kadtest.StrAddr])(nil)
// 	// _ core.FindClosestDHT[key.Key8, kadtest.StrAddr] = (*Dht[key.Key8, kadtest.StrAddr])(nil)
// 	_ core.QueryDHT[key.Key8, kadtest.StrAddr] = (*Dht[key.Key8, kadtest.StrAddr])(nil)
// )

// func TestExhaustiveQuery(t *testing.T) {
// 	ctx, cancel := kadtest.CtxShort(t)
// 	defer cancel()

// 	clk := clock.NewMock()
// 	_, nodes := nettest.LinearTopology(4, clk)
// 	ccfg := DefaultConfig()
// 	ccfg.Clock = clk
// 	ccfg.PeerstoreTTL = peerstoreTTL

// 	// A (ids[0]) is looking for D (ids[3])
// 	// A will first ask B, B will reply with C's address (and A's address)
// 	// A will then ask C, C will reply with D's address (and B's address)
// 	self := nodes[0].NodeInfo.ID()
// 	c, err := NewDht[key.Key8, kadtest.StrAddr](self, nodes[0].Router, nodes[0].RoutingTable, ccfg)
// 	require.NoError(t, err)

// 	target := nodes[3].NodeInfo.ID().Key()

// 	visited := make(map[string]int)

// 	// Record the nodes as they are visited
// 	qfn := func(ctx context.Context, node core.Node[key.Key8, kadtest.StrAddr], stats core.QueryStats) error {
// 		visited[node.ID().String()]++
// 		return nil
// 	}

// 	// Run a query to find the value
// 	_, err = c.Query(ctx, target, qfn)
// 	require.NoError(t, err)

// 	require.Equal(t, 3, len(visited))
// 	require.Contains(t, visited, nodes[1].NodeInfo.ID().String())
// 	require.Contains(t, visited, nodes[2].NodeInfo.ID().String())
// 	require.Contains(t, visited, nodes[3].NodeInfo.ID().String())
// }

// func TestRoutingUpdatedEventEmittedForCloserNodes(t *testing.T) {
// 	ctx, cancel := kadtest.CtxShort(t)
// 	defer cancel()

// 	clk := clock.NewMock()
// 	_, nodes := nettest.LinearTopology(4, clk)

// 	ccfg := DefaultConfig()
// 	ccfg.Clock = clk
// 	ccfg.PeerstoreTTL = peerstoreTTL

// 	// A (ids[0]) is looking for D (ids[3])
// 	// A will first ask B, B will reply with C's address (and A's address)
// 	// A will then ask C, C will reply with D's address (and B's address)
// 	self := nodes[0].NodeInfo.ID()
// 	c, err := NewDht[key.Key8, kadtest.StrAddr](self, nodes[0].Router, nodes[0].RoutingTable, ccfg)
// 	if err != nil {
// 		log.Fatalf("unexpected error creating dht: %v", err)
// 	}

// 	buffer := make(chan RoutingNotification, 5)
// 	go func() {
// 		for {
// 			select {
// 			case <-ctx.Done():
// 				return
// 			case ev := <-c.RoutingNotifications():
// 				buffer <- ev
// 			}
// 		}
// 	}()

// 	qfn := func(ctx context.Context, node core.Node[key.Key8, kadtest.StrAddr], stats core.QueryStats) error {
// 		return nil
// 	}

// 	// Run a query to find the value
// 	target := nodes[3].NodeInfo.ID().Key()
// 	_, err = c.Query(ctx, target, qfn)
// 	require.NoError(t, err)

// 	// the query run by the dht should have received a response from nodes[1] with closer nodes
// 	// nodes[0] and nodes[2] which should trigger a routing table update since nodes[2] was
// 	// not in the dht's routing table.
// 	ev, err := expectEventType(t, ctx, buffer, &EventRoutingUpdated[key.Key8, kadtest.StrAddr]{})
// 	require.NoError(t, err)

// 	tev := ev.(*EventRoutingUpdated[key.Key8, kadtest.StrAddr])
// 	require.Equal(t, nodes[2].NodeInfo.ID(), tev.NodeInfo.ID())

// 	// no EventRoutingUpdated is sent for the self node

// 	// the query continues and should have received a response from nodes[2] with closer nodes
// 	// nodes[1] and nodes[3] which should trigger a routing table update since nodes[3] was
// 	// not in the dht's routing table.
// 	ev, err = expectEventType(t, ctx, buffer, &EventRoutingUpdated[key.Key8, kadtest.StrAddr]{})
// 	require.NoError(t, err)

// 	tev = ev.(*EventRoutingUpdated[key.Key8, kadtest.StrAddr])
// 	require.Equal(t, nodes[3].NodeInfo.ID(), tev.NodeInfo.ID())
// }

// func TestBootstrap(t *testing.T) {
// 	ctx, cancel := kadtest.CtxShort(t)
// 	defer cancel()

// 	clk := clock.NewMock()
// 	_, nodes := nettest.LinearTopology(4, clk)

// 	ccfg := DefaultConfig()
// 	ccfg.Clock = clk
// 	ccfg.PeerstoreTTL = peerstoreTTL

// 	self := nodes[0].NodeInfo.ID()
// 	d, err := NewDht[key.Key8, kadtest.StrAddr](self, nodes[0].Router, nodes[0].RoutingTable, ccfg)
// 	if err != nil {
// 		log.Fatalf("unexpected error creating dht: %v", err)
// 	}

// 	buffer := make(chan RoutingNotification, 5)
// 	go func() {
// 		for {
// 			select {
// 			case <-ctx.Done():
// 				return
// 			case ev := <-d.RoutingNotifications():
// 				buffer <- ev
// 			}
// 		}
// 	}()

// 	seeds := []kad.NodeID[key.Key8]{
// 		nodes[1].NodeInfo.ID(),
// 	}
// 	err = d.Bootstrap(ctx, seeds)
// 	require.NoError(t, err)

// 	// the query run by the dht should have completed
// 	ev, err := expectEventType(t, ctx, buffer, &EventBootstrapFinished{})
// 	require.NoError(t, err)

// 	require.IsType(t, &EventBootstrapFinished{}, ev)
// 	tevf := ev.(*EventBootstrapFinished)
// 	require.Equal(t, 3, tevf.Stats.Requests)
// 	require.Equal(t, 3, tevf.Stats.Success)
// 	require.Equal(t, 0, tevf.Stats.Failure)

// 	// DHT should now have node1 in its routing table
// 	_, err = d.GetNode(ctx, nodes[1].NodeInfo.ID())
// 	require.NoError(t, err)

// 	// DHT should now have node2 in its routing table
// 	_, err = d.GetNode(ctx, nodes[2].NodeInfo.ID())
// 	require.NoError(t, err)

// 	// DHT should now have node3 in its routing table
// 	_, err = d.GetNode(ctx, nodes[3].NodeInfo.ID())
// 	require.NoError(t, err)
// }

// func TestIncludeNode(t *testing.T) {
// 	ctx, cancel := kadtest.CtxShort(t)
// 	defer cancel()

// 	clk := clock.NewMock()
// 	_, nodes := nettest.LinearTopology(4, clk)

// 	ccfg := DefaultConfig()
// 	ccfg.Clock = clk
// 	ccfg.PeerstoreTTL = peerstoreTTL

// 	candidate := nodes[len(nodes)-1].NodeInfo // not in nodes[0] routing table

// 	self := nodes[0].NodeInfo.ID()
// 	d, err := NewDht[key.Key8, kadtest.StrAddr](self, nodes[0].Router, nodes[0].RoutingTable, ccfg)
// 	if err != nil {
// 		log.Fatalf("unexpected error creating dht: %v", err)
// 	}

// 	// the routing table should not contain the node yet
// 	_, err = d.GetNode(ctx, candidate.ID())
// 	require.ErrorIs(t, err, core.ErrNodeNotFound)

// 	events := d.RoutingNotifications()

// 	// inject a new node into the dht's includeEvents queue
// 	err = d.AddNodes(ctx, []kad.NodeInfo[key.Key8, kadtest.StrAddr]{candidate})
// 	require.NoError(t, err)

// 	// the include state machine runs in the background and eventually should add the node to routing table
// 	ev, err := expectEventType(t, ctx, events, &EventRoutingUpdated[key.Key8, kadtest.StrAddr]{})
// 	require.NoError(t, err)

// 	tev := ev.(*EventRoutingUpdated[key.Key8, kadtest.StrAddr])
// 	require.Equal(t, candidate.ID(), tev.NodeInfo.ID())

// 	// the routing table should not contain the node yet
// 	_, err = d.GetNode(ctx, candidate.ID())
// 	require.NoError(t, err)
// }
