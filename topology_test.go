package zikade

import (
	"context"
	"testing"
	"time"

	"github.com/benbjohnson/clock"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	mocknet "github.com/libp2p/go-libp2p/p2p/net/mock"
	"github.com/stretchr/testify/require"

	"github.com/probe-lab/zikade/internal/coord"
	"github.com/probe-lab/zikade/kadt"
)

// A Topology is an arrangement of DHTs intended to simulate a network.
//
// Hosts are created on a [mocknet.Mocknet] rather than on real sockets. Besides
// being faster and leaving no file descriptors behind, this avoids a hang that
// go-libp2p 0.32 introduced when two hosts dial each other simultaneously over
// loopback, which is exactly what Connect provokes.
type Topology struct {
	clk  clock.Clock
	tb   testing.TB
	mn   mocknet.Mocknet
	dhts map[string]*DHT
	rns  map[string]*coord.BufferedRoutingNotifier
}

// NewBubbleTopology returns a Topology for a test running inside a
// [testing/synctest] bubble. Everything it builds must be built inside the
// bubble so that the goroutines belong to it.
//
// It exists to schedule one extra step at teardown. Each DHT owns an in-memory
// leveldb whose pool drain goroutine finishes shutting down by waiting on
// time.After(time.Second). That timer belongs to the bubble, and time stops
// advancing the moment the root goroutine exits, so without a final sleep on
// fake time the goroutine never exits and synctest.Test reports a deadlock.
// Cleanup functions run last-registered-first, so registering the sleep before
// any DHT exists puts it after every close.
func NewBubbleTopology(tb testing.TB) *Topology {
	tb.Cleanup(func() { time.Sleep(2 * time.Second) })

	return NewTopology(tb)
}

func NewTopology(tb testing.TB) *Topology {
	mn := mocknet.New()
	tb.Cleanup(func() {
		if err := mn.Close(); err != nil {
			tb.Logf("unexpected error when closing mocknet: %s", err)
		}
	})

	return &Topology{
		clk:  clock.New(),
		tb:   tb,
		mn:   mn,
		dhts: make(map[string]*DHT),
		rns:  make(map[string]*coord.BufferedRoutingNotifier),
	}
}

// newHost adds a host to the mocknet and links it to every other host already
// present. Linking is the ability to dial, not a connection: on a real network
// any host can dial any other, and a query is free to hop to a peer it has only
// just heard about. Linking everything preserves that, leaving Connect to
// control routing table contents alone.
func (t *Topology) newHost() host.Host {
	t.tb.Helper()

	h, err := t.mn.GenPeer()
	require.NoError(t.tb, err)

	require.NoError(t.tb, t.mn.LinkAll())

	return h
}

func (t *Topology) SetClock(clk clock.Clock) {
	t.clk = clk
}

// AddServer adds a DHT configured as a server to the topology.
// If cfg is nil the default DHT config is used with Mode set to ModeOptServer
func (t *Topology) AddServer(cfg *Config) *DHT {
	t.tb.Helper()

	h := t.newHost()

	if cfg == nil {
		cfg = DefaultConfig()
	}
	cfg.Mode = ModeOptServer

	d, err := New(h, cfg)
	require.NoError(t.tb, err)

	rn := coord.NewBufferedRoutingNotifier()
	d.kad.SetRoutingNotifier(rn)

	t.tb.Cleanup(func() {
		if err = d.Close(); err != nil {
			t.tb.Logf("unexpected error when closing dht: %s", err)
		}
	})

	did := t.makeid(d)
	t.dhts[did] = d
	t.rns[did] = rn

	return d
}

// AddServer adds a DHT configured as a client to the topology.
// If cfg is nil the default DHT config is used with Mode set to ModeOptClient
func (t *Topology) AddClient(cfg *Config) *DHT {
	t.tb.Helper()

	h := t.newHost()

	if cfg == nil {
		cfg = DefaultConfig()
	}
	cfg.Mode = ModeOptClient

	d, err := New(h, cfg)
	require.NoError(t.tb, err)

	rn := coord.NewBufferedRoutingNotifier()
	d.kad.SetRoutingNotifier(rn)

	t.tb.Cleanup(func() {
		if err = d.Close(); err != nil {
			t.tb.Logf("unexpected error when closing dht: %s", err)
		}
	})

	did := t.makeid(d)
	t.dhts[did] = d
	t.rns[did] = rn

	return d
}

func (t *Topology) makeid(d *DHT) string {
	return kadt.PeerID(d.host.ID()).String()
}

// Connect ensures that a has b in its routing table and vice versa.
func (t *Topology) Connect(ctx context.Context, a *DHT, b *DHT) {
	t.tb.Helper()

	aid := t.makeid(a)
	arn, ok := t.rns[aid]
	require.True(t.tb, ok, "expected routing notifier for supplied DHT")

	aAddr := peer.AddrInfo{
		ID:    a.host.ID(),
		Addrs: a.host.Addrs(),
	}

	bid := t.makeid(b)
	brn, ok := t.rns[bid]
	require.True(t.tb, ok, "expected routing notifier for supplied DHT")

	bAddr := peer.AddrInfo{
		ID:    b.host.ID(),
		Addrs: b.host.Addrs(),
	}

	// Add b's addresses to a
	err := a.AddAddresses(ctx, []peer.AddrInfo{bAddr}, time.Hour)
	require.NoError(t.tb, err)

	// Add a's addresses to b
	err = b.AddAddresses(ctx, []peer.AddrInfo{aAddr}, time.Hour)
	require.NoError(t.tb, err)

	// include state machine runs in the background for a and eventually should add the node to routing table
	_, err = arn.ExpectRoutingUpdated(ctx, kadt.PeerID(b.host.ID()))
	require.NoError(t.tb, err)

	// the routing table should now contain the node
	require.True(t.tb, a.kad.IsRoutable(ctx, kadt.PeerID(b.host.ID())))

	// include state machine runs in the background for b and eventually should add the node to routing table
	_, err = brn.ExpectRoutingUpdated(ctx, kadt.PeerID(a.host.ID()))
	require.NoError(t.tb, err)

	// the routing table should now contain the node
	require.True(t.tb, b.kad.IsRoutable(ctx, kadt.PeerID(a.host.ID())))
}

// Isolate makes d unreachable by every other DHT in the topology, and them
// unreachable by it, so that any request to or from d fails.
//
// Closing the host is not enough on a mocknet: existing connections remain
// usable afterwards and requests continue to be served. Removing the links is
// the harness equivalent of pulling the cable. Routing tables are left
// untouched, so callers can assert on eviction.
func (t *Topology) Isolate(d *DHT) {
	t.tb.Helper()

	id := d.host.ID()
	for _, other := range t.dhts {
		otherID := other.host.ID()
		if otherID == id {
			continue
		}

		if err := t.mn.DisconnectPeers(id, otherID); err != nil {
			t.tb.Fatalf("disconnecting %s from %s: %s", id, otherID, err)
		}
		if err := t.mn.UnlinkPeers(id, otherID); err != nil {
			t.tb.Fatalf("unlinking %s from %s: %s", id, otherID, err)
		}
	}
}

// ConnectChain connects the DHTs in a linear chain.
// The DHTs are configured with routing tables that contain immediate neighbours,
// such that DHT[x] has DHT[x-1] and DHT[x+1] in its routing table.
// The connections do not form a ring: DHT[0] only has DHT[1] in its table and DHT[n-1] only has DHT[n-2] in its table.
// If n > 2 then the first and last DHTs are guaranteed not have one another in their routing tables.
func (t *Topology) ConnectChain(ctx context.Context, ds ...*DHT) {
	for i := 1; i < len(ds); i++ {
		t.Connect(ctx, ds[i-1], ds[i])
	}
}

// ExpectRoutingUpdated blocks until an [EventRoutingUpdated] event is emitted by the supplied [DHT] the specified peer id.
func (t *Topology) ExpectRoutingUpdated(ctx context.Context, d *DHT, id peer.ID) (*coord.EventRoutingUpdated, error) {
	did := t.makeid(d)
	rn, ok := t.rns[did]
	require.True(t.tb, ok, "expected routing notifier for supplied DHT")

	return rn.ExpectRoutingUpdated(ctx, kadt.PeerID(id))
}

// ExpectRoutingRemoved blocks until an [EventRoutingRemoved] event is emitted by the supplied [DHT] the specified peer id.
func (t *Topology) ExpectRoutingRemoved(ctx context.Context, d *DHT, id peer.ID) (*coord.EventRoutingRemoved, error) {
	did := t.makeid(d)
	rn, ok := t.rns[did]
	require.True(t.tb, ok, "expected routing notifier for supplied DHT")

	return rn.ExpectRoutingRemoved(ctx, kadt.PeerID(id))
}
