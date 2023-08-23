package libp2p

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/benbjohnson/clock"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/net/swarm"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
	"github.com/stretchr/testify/require"

	"github.com/plprobelab/go-kademlia/event"
	"github.com/plprobelab/go-kademlia/kad"
	"github.com/plprobelab/go-kademlia/key"
	"github.com/plprobelab/go-kademlia/network/address"
	"github.com/plprobelab/go-kademlia/network/endpoint"
	"github.com/plprobelab/go-kademlia/sim"

	"github.com/iand/zikade/internal/kadtest"
)

var peerstoreTTL = 10 * time.Minute

func createEndpoints(t *testing.T, ctx context.Context, nPeers int) (
	[]*Libp2pEndpoint, []*AddrInfo, []*PeerID,
	[]event.AwareScheduler,
) {
	clk := clock.New()

	scheds := make([]event.AwareScheduler, nPeers)
	ids := make([]*PeerID, nPeers)
	addrinfos := make([]*AddrInfo, nPeers)
	endpoints := make([]*Libp2pEndpoint, nPeers)
	for i := 0; i < nPeers; i++ {
		scheds[i] = event.NewSimpleScheduler(clk)
		host, err := libp2p.New()
		require.NoError(t, err)
		ids[i] = NewPeerID(host.ID())
		addrinfos[i] = NewAddrInfo(peer.AddrInfo{
			ID:    ids[i].ID,
			Addrs: host.Addrs(),
		})
		endpoints[i] = NewLibp2pEndpoint(ctx, host, scheds[i])
		na, err := endpoints[i].NetworkAddress(ids[i])
		require.NoError(t, err)
		for _, a := range na.Addresses() {
			require.Contains(t, host.Addrs(), a)
		}
	}
	return endpoints, addrinfos, ids, scheds
}

func connectEndpoints(t *testing.T, ctx context.Context, endpoints []*Libp2pEndpoint,
	addrInfos []*AddrInfo,
) {
	require.Len(t, endpoints, len(addrInfos))
	for i, ep := range endpoints {
		for j, ai := range addrInfos {
			if i == j {
				continue
			}
			err := ep.MaybeAddToPeerstore(ctx, ai, peerstoreTTL)
			require.NoError(t, err)
		}
	}
}

func createRouters(t *testing.T, ctx context.Context, nPeers int) ([]*Router, []*AddrInfo, []*PeerID) {
	ids := make([]*PeerID, nPeers)
	addrinfos := make([]*AddrInfo, nPeers)
	routers := make([]*Router, nPeers)
	for i := 0; i < nPeers; i++ {
		host, err := libp2p.New()
		require.NoError(t, err)
		ids[i] = NewPeerID(host.ID())
		addrinfos[i] = NewAddrInfo(peer.AddrInfo{
			ID:    ids[i].ID,
			Addrs: host.Addrs(),
		})
		routers[i] = NewRouter(ctx, host, 10*time.Minute)
		na, err := routers[i].GetNodeInfo(ctx, ids[i])
		require.NoError(t, err)
		for _, a := range na.Addresses() {
			require.Contains(t, host.Addrs(), a)
		}
	}
	return routers, addrinfos, ids
}

func connectRouters(t *testing.T, ctx context.Context, routers []*Router, addrInfos []*AddrInfo) {
	require.Len(t, routers, len(addrInfos))
	for i, r := range routers {
		for j, ai := range addrInfos {
			if i == j {
				continue
			}
			err := r.AddNodeInfo(ctx, ai, peerstoreTTL)
			require.NoError(t, err)
		}
	}
}

func TestConnections(t *testing.T) {
	ctx, cancel := kadtest.CtxShort(t)
	defer cancel()

	// create routers
	routers, addrs, ids := createRouters(t, ctx, 4)

	invalidID := kadtest.NewInfo[key.Key256, ma.Multiaddr](kadtest.NewID(kadtest.NewStringID("invalid").Key()), nil)

	// test that the endpoint's kademlia key is as expected
	for i, r := range routers {
		require.Equal(t, ids[i].Key(), r.Key())
	}

	// add peer 1 to peer 0's peerstore
	err := routers[0].AddNodeInfo(ctx, addrs[1], peerstoreTTL)
	require.NoError(t, err)
	// add invalid peer to peerstore
	err = routers[0].AddNodeInfo(ctx, invalidID, peerstoreTTL)
	require.ErrorIs(t, err, ErrInvalidPeer)
	// should add no information for self
	err = routers[0].AddNodeInfo(ctx, addrs[0], peerstoreTTL)
	require.NoError(t, err)
	// add peer 1 to peer 0's peerstore
	err = routers[0].AddNodeInfo(ctx, addrs[3], peerstoreTTL)
	require.NoError(t, err)

	// test connectedness, not connected but known address -> NotConnected
	connectedness, err := routers[0].Connectedness(ids[1])
	require.NoError(t, err)
	require.Equal(t, endpoint.NotConnected, connectedness)
	// not connected, unknown address -> NotConnected
	connectedness, err = routers[0].Connectedness(ids[2])
	require.NoError(t, err)
	require.Equal(t, endpoint.NotConnected, connectedness)

	// verify network address for valid peerid
	netAddr, err := routers[0].GetNodeInfo(ctx, ids[1])
	require.NoError(t, err)
	ai, ok := netAddr.(*AddrInfo)
	require.True(t, ok)
	require.Equal(t, ids[1].ID, ai.PeerID().ID)
	require.Len(t, ai.Addrs, len(addrs[1].Addrs))
	for _, addr := range ai.Addrs {
		require.Contains(t, addrs[1].Addrs, addr)
	}
	// dial from 0 to 1
	err = routers[0].DialPeer(ctx, ids[1])
	require.NoError(t, err)
	// test connectedness
	connectedness, err = routers[0].Connectedness(ids[1])
	require.NoError(t, err)
	require.Equal(t, endpoint.Connected, connectedness)

	// test peerinfo
	peerinfo, err := routers[0].PeerInfo(ids[1])
	require.NoError(t, err)
	// filter out loopback addresses
	expectedAddrs := ma.FilterAddrs(addrs[1].Addrs, func(a ma.Multiaddr) bool {
		return !manet.IsIPLoopback(a)
	})
	for _, addr := range expectedAddrs {
		require.Contains(t, peerinfo.Addrs, addr, addr.String(), expectedAddrs)
	}
	peerinfo, err = routers[0].PeerInfo(ids[2])
	require.NoError(t, err)
	require.Len(t, peerinfo.Addrs, 0)

	// dial from 1 to invalid peerid
	// dial again from 0 to 1, no is already connected
	err = routers[0].DialPeer(ctx, ids[1])
	require.NoError(t, err)
	// dial from 1 to 0, works because 1 is already connected to 0
	err = routers[1].DialPeer(ctx, ids[0])
	require.NoError(t, err)
	// dial from 0 to 2, fails because 0 doesn't know 2's addresses
	err = routers[0].DialPeer(ctx, ids[2])
	require.Error(t, err)
}

func TestRequestHandler(t *testing.T) {
	ctx, cancel := kadtest.CtxShort(t)
	defer cancel()

	endpoints, _, _, _ := createEndpoints(t, ctx, 1)

	// set node 1 to server mode
	requestHandler := func(ctx context.Context, id kad.NodeID[key.Key256],
		req kad.Message,
	) (kad.Message, error) {
		// request handler returning the received message
		return req, nil
	}
	err := endpoints[0].AddRequestHandler(ProtocolKad1, &Message{}, requestHandler)
	require.NoError(t, err)
	err = endpoints[0].AddRequestHandler(ProtocolKad1, &Message{}, nil)
	require.Equal(t, endpoint.ErrNilRequestHandler, err)

	// invalid message format for handler
	err = endpoints[0].AddRequestHandler("/fail/1.0.0", &sim.Message[key.Key256, ma.Multiaddr]{}, requestHandler)
	require.Equal(t, ErrRequireProtoKadMessage, err)

	// remove request handler
	endpoints[0].RemoveRequestHandler(ProtocolKad1)
}

func TestSendMessageFailFast(t *testing.T) {
	ctx, cancel := kadtest.CtxShort(t)
	defer cancel()

	routers, addrs, ids := createRouters(t, ctx, 3)
	connectRouters(t, ctx, routers[:2], addrs[:2])

	t.Run("invalid request format", func(t *testing.T) {
		// invalid request format (not protobuf)
		_, err := routers[0].SendMessage(ctx, addrs[1], ProtocolKad1, &sim.Message[key.Key256, ma.Multiaddr]{})
		require.Equal(t, ErrRequireProtoKadMessage, err)
	})

	t.Run("invalid protocol id", func(t *testing.T) {
		// invalid request format (not protobuf)
		req := FindPeerRequest(ids[1])
		_, err := routers[0].SendMessage(ctx, addrs[1], address.ProtocolID("/test/1.0.0"), req)
		require.Equal(t, ErrInvalidProtocol, err)
	})

	t.Run("invalid recipient", func(t *testing.T) {
		// invalid recipient (not a peerid)
		req := FindPeerRequest(ids[1])
		_, err := routers[0].SendMessage(ctx, kadtest.NewInfo[key.Key256, ma.Multiaddr](kadtest.NewID(kadtest.NewStringID("invalid").Key()), nil), ProtocolKad1, req)
		require.ErrorIs(t, err, ErrInvalidPeer)
	})

	t.Run("not connected", func(t *testing.T) {
		// peer 0 isn't connected to peer 2, so it should fail fast
		req := FindPeerRequest(ids[1])
		_, err := routers[0].SendMessage(ctx, addrs[2], ProtocolKad1, req)
		require.Equal(t, ErrUnknownPeer, err)
	})
}

func TestSuccessfulRequest(t *testing.T) {
	ctx, cancel := kadtest.CtxShort(t)
	defer cancel()

	endpoints, addrs, ids, scheds := createEndpoints(t, ctx, 2)
	connectEndpoints(t, ctx, endpoints, addrs)

	// set node 1 to server mode
	requestHandler := func(ctx context.Context, id kad.NodeID[key.Key256],
		req kad.Message,
	) (kad.Message, error) {
		// request handler returning the received message
		return req, nil
	}
	err := endpoints[1].AddRequestHandler(ProtocolKad1, &Message{}, requestHandler)
	require.NoError(t, err)

	wg := sync.WaitGroup{}
	responseHandler := func(ctx context.Context,
		resp kad.Response[key.Key256, ma.Multiaddr], err error,
	) {
		wg.Done()
	}
	req := FindPeerRequest(ids[1])
	wg.Add(1)
	endpoints[0].SendRequestHandleResponse(ctx, ProtocolKad1, ids[1], req,
		&Message{}, time.Second, responseHandler)
	wg.Add(2)
	go func() {
		// run server 1
		for !scheds[1].RunOne(ctx) {
			time.Sleep(time.Millisecond)
		}
		require.False(t, scheds[1].RunOne(ctx)) // only 1 action should run on server
		wg.Done()
	}()
	go func() {
		// timeout is queued in the scheduler 0
		for !scheds[0].RunOne(ctx) {
			time.Sleep(time.Millisecond)
		}
		require.False(t, scheds[0].RunOne(ctx))
		wg.Done()
	}()
	wg.Wait()
	// nothing to run for both schedulers
	for _, s := range scheds {
		require.Equal(t, event.MaxTime, s.NextActionTime(ctx))
	}
}

func TestSuccessfulRequestSync(t *testing.T) {
	ctx, cancel := kadtest.CtxShort(t)
	defer cancel()

	endpoints, addrs, ids, scheds := createEndpoints(t, ctx, 2)
	connectEndpoints(t, ctx, endpoints, addrs)

	// set node 1 to server mode
	requestHandler := func(ctx context.Context, id kad.NodeID[key.Key256],
		req kad.Message,
	) (kad.Message, error) {
		// request handler returning the received message
		return req, nil
	}
	err := endpoints[1].AddRequestHandler(ProtocolKad1, &Message{}, requestHandler)
	require.NoError(t, err)

	wg := sync.WaitGroup{}
	req := FindPeerRequest(ids[1])
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := endpoints[0].SendMessage(ctx, ids[1], ProtocolKad1, req)
		require.NoError(t, err)
	}()
	wg.Add(1)
	go func() {
		// run server 1
		for !scheds[1].RunOne(ctx) {
			time.Sleep(time.Millisecond)
		}
		require.False(t, scheds[1].RunOne(ctx)) // only 1 action should run on server
		wg.Done()
	}()
	wg.Wait()
	// nothing to run for both schedulers
	for _, s := range scheds {
		require.Equal(t, event.MaxTime, s.NextActionTime(ctx))
	}
}

func TestReqUnknownPeer(t *testing.T) {
	ctx, cancel := kadtest.CtxShort(t)
	defer cancel()

	// create endpoints
	endpoints, addrs, ids, scheds := createEndpoints(t, ctx, 2)
	// replace address of peer 1 with an invalid address
	addrs[1] = NewAddrInfo(peer.AddrInfo{
		ID:    addrs[1].PeerID().ID,
		Addrs: []ma.Multiaddr{ma.StringCast("/ip4/1.2.3.4")},
	})
	// "connect" the endpoints. 0 will store an invalid address for 1, so it
	// won't fail fast, because it believes that it knows how to reach 1
	connectEndpoints(t, ctx, endpoints, addrs)

	// verfiy that the wrong address is stored for peer 1 in peer 0
	na, err := endpoints[0].NetworkAddress(ids[1])
	require.NoError(t, err)
	for _, addr := range na.Addresses() {
		require.Contains(t, addrs[1].Addrs, addr)
	}

	req := FindPeerRequest(ids[1])
	wg := sync.WaitGroup{}
	responseHandler := func(_ context.Context, _ kad.Response[key.Key256, ma.Multiaddr], err error) {
		wg.Done()
		require.Equal(t, swarm.ErrNoGoodAddresses, err)
	}

	// unknown valid peerid (address not stored in peerstore)
	wg.Add(1)
	err = endpoints[0].SendRequestHandleResponse(ctx, ProtocolKad1, ids[1], req,
		&Message{}, time.Second, responseHandler)
	require.NoError(t, err)
	wg.Add(1)
	go func() {
		// timeout is queued in the scheduler 0
		for !scheds[0].RunOne(ctx) {
			time.Sleep(time.Millisecond)
		}
		require.False(t, scheds[0].RunOne(ctx))
		wg.Done()
	}()
	wg.Wait()
	// nothing to run for both schedulers
	for _, s := range scheds {
		require.Equal(t, event.MaxTime, s.NextActionTime(ctx))
	}
}

// func TestReqTimeout(t *testing.T) {
// ctx, cancel := kadtest.CtxShort(t)
// defer cancel()

// 	endpoints, addrs, ids, scheds := createEndpoints(t, ctx, 2)
// 	connectEndpoints(t, ctx, endpoints, addrs)

// 	req := FindPeerRequest(ids[1])

// 	wg := sync.WaitGroup{}

// 	err := endpoints[1].AddRequestHandler(ProtocolKad1, &Message{}, func(ctx context.Context,
// 		id kad.NodeID[key.Key256], req kad.Message,
// 	) (kad.Message, error) {
// 		return req, nil
// 	})
// 	require.NoError(t, err)

// 	responseHandler := func(_ context.Context, _ kad.Response[key.Key256, ma.Multiaddr], err error) {
// 		require.Error(t, err)
// 		wg.Done()
// 	}
// 	// timeout after 100 ms, will fail immediately
// 	err = endpoints[0].SendRequestHandleResponse(ctx, ProtocolKad1, ids[1], req,
// 		&Message{}, 100*time.Millisecond, responseHandler)
// 	require.NoError(t, err)
// 	wg.Add(2)
// 	go func() {
// 		// timeout is queued in the scheduler 0
// 		for !scheds[0].RunOne(ctx) {
// 			time.Sleep(1 * time.Millisecond)
// 		}
// 		require.False(t, scheds[0].RunOne(ctx))
// 		wg.Done()
// 	}()
// 	wg.Wait()
// 	// now that the client has timed out, the server will send back the reply
// 	require.True(t, scheds[1].RunOne(ctx))

// 	// nothing to run for both schedulers
// 	for _, s := range scheds {
// 		require.Equal(t, event.MaxTime, s.NextActionTime(ctx))
// 	}
// }

func TestReqHandlerError(t *testing.T) {
	// server request handler error
	ctx, cancel := kadtest.CtxShort(t)
	defer cancel()

	endpoints, addrs, ids, scheds := createEndpoints(t, ctx, 2)
	connectEndpoints(t, ctx, endpoints, addrs)

	req := FindPeerRequest(ids[1])

	wg := sync.WaitGroup{}

	err := endpoints[1].AddRequestHandler(ProtocolKad1, &Message{}, func(ctx context.Context,
		id kad.NodeID[key.Key256], req kad.Message,
	) (kad.Message, error) {
		// request handler returns error
		return nil, errors.New("server error")
	})
	require.NoError(t, err)
	// responseHandler is run after context is cancelled
	responseHandler := func(ctx context.Context,
		resp kad.Response[key.Key256, ma.Multiaddr], err error,
	) {
		wg.Done()
		require.Error(t, err)
	}
	noResponseCtx, cancel := context.WithCancel(ctx)
	wg.Add(1)
	err = endpoints[0].SendRequestHandleResponse(noResponseCtx, ProtocolKad1, ids[1], req,
		&Message{}, 0, responseHandler)
	require.NoError(t, err)
	wg.Add(2)
	go func() {
		for !scheds[1].RunOne(ctx) {
			time.Sleep(time.Millisecond)
		}
		require.False(t, scheds[1].RunOne(ctx))
		cancel()
		wg.Done()
	}()
	go func() {
		// timeout is queued in the scheduler 0
		for !scheds[0].RunOne(ctx) {
			time.Sleep(time.Millisecond)
		}
		require.False(t, scheds[0].RunOne(ctx))
		wg.Done()
	}()
	wg.Wait()
	// nothing to run for both schedulers
	for _, s := range scheds {
		require.Equal(t, event.MaxTime, s.NextActionTime(ctx))
	}
}

func TestReqHandlerReturnsWrongType(t *testing.T) {
	// server request handler returns wrong message type, no response is
	// returned by the server
	ctx, cancel := kadtest.CtxShort(t)
	defer cancel()

	endpoints, addrs, ids, scheds := createEndpoints(t, ctx, 2)
	connectEndpoints(t, ctx, endpoints, addrs)

	req := FindPeerRequest(ids[1])

	wg := sync.WaitGroup{}
	err := endpoints[1].AddRequestHandler(ProtocolKad1, &Message{}, func(ctx context.Context,
		id kad.NodeID[key.Key256], req kad.Message,
	) (kad.Message, error) {
		// request handler returns error
		return &sim.Message[key.Key256, ma.Multiaddr]{}, nil
	})
	require.NoError(t, err)
	// responseHandler is run after context is cancelled
	responseHandler := func(ctx context.Context,
		resp kad.Response[key.Key256, ma.Multiaddr], err error,
	) {
		wg.Done()
		require.Error(t, err)
	}
	noResponseCtx, cancel := context.WithCancel(ctx)
	wg.Add(1)
	err = endpoints[0].SendRequestHandleResponse(noResponseCtx, ProtocolKad1, ids[1], req,
		&Message{}, 0, responseHandler)
	require.NoError(t, err)
	wg.Add(2)
	go func() {
		for !scheds[1].RunOne(ctx) {
			time.Sleep(time.Millisecond)
		}
		require.False(t, scheds[1].RunOne(ctx))
		cancel()
		wg.Done()
	}()
	go func() {
		// timeout is queued in the scheduler 0
		for !scheds[0].RunOne(ctx) {
			time.Sleep(time.Millisecond)
		}
		require.False(t, scheds[0].RunOne(ctx))
		wg.Done()
	}()
	wg.Wait()
	// nothing to run for both schedulers
	for _, s := range scheds {
		require.Equal(t, event.MaxTime, s.NextActionTime(ctx))
	}
}
