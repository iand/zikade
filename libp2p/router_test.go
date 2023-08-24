package libp2p

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/net/swarm"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
	"github.com/stretchr/testify/require"

	"github.com/plprobelab/go-kademlia/kad"
	"github.com/plprobelab/go-kademlia/key"
	"github.com/plprobelab/go-kademlia/network/address"
	"github.com/plprobelab/go-kademlia/network/endpoint"
	"github.com/plprobelab/go-kademlia/sim"

	"github.com/iand/zikade/internal/kadtest"
)

var peerstoreTTL = 10 * time.Minute

func newTestRouter(t *testing.T) *Router {
	host, err := libp2p.New()
	require.NoError(t, err)
	return NewRouter(host, peerstoreTTL)
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
		routers[i] = NewRouter(host, peerstoreTTL)
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
	connectedness, err := routers[0].Connectedness(ctx, ids[1])
	require.NoError(t, err)
	require.Equal(t, endpoint.NotConnected, connectedness)
	// not connected, unknown address -> NotConnected
	connectedness, err = routers[0].Connectedness(ctx, ids[2])
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
	connectedness, err = routers[0].Connectedness(ctx, ids[1])
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

	routers, _, _ := createRouters(t, ctx, 3)

	// set node 1 to server mode
	requestHandler := func(ctx context.Context, id kad.NodeID[key.Key256],
		req kad.Message,
	) (kad.Message, error) {
		// request handler returning the received message
		return req, nil
	}
	err := routers[0].AddRequestHandler(ProtocolKad1, &Message{}, requestHandler)
	require.NoError(t, err)

	err = routers[0].AddRequestHandler(ProtocolKad1, &Message{}, nil)
	require.Equal(t, ErrInvalidRequestHandler, err)

	// invalid message format for handler
	err = routers[0].AddRequestHandler("/fail/1.0.0", &sim.Message[key.Key256, ma.Multiaddr]{}, requestHandler)
	require.Equal(t, ErrRequireProtoKadMessage, err)

	// remove request handler
	routers[0].RemoveRequestHandler(ProtocolKad1)
}

func TestSuccessfulRequest(t *testing.T) {
	ctx, cancel := kadtest.CtxShort(t)
	defer cancel()

	routers, addrs, ids := createRouters(t, ctx, 3)
	connectRouters(t, ctx, routers[:2], addrs[:2])

	// set node 1 to server mode
	requestHandler := func(ctx context.Context, id kad.NodeID[key.Key256],
		req kad.Message,
	) (kad.Message, error) {
		// request handler returning the received message
		return req, nil
	}
	err := routers[1].AddRequestHandler(ProtocolKad1, &Message{}, requestHandler)
	require.NoError(t, err)

	req := FindPeerRequest(ids[1])
	_, err = routers[0].SendMessage(ctx, addrs[1], ProtocolKad1, req)
	require.NoError(t, err)
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

	// t.Run("not connected", func(t *testing.T) {
	// 	// peer 0 isn't connected to peer 2, so it should fail fast
	// 	req := FindPeerRequest(ids[1])
	// 	_, err := routers[0].SendMessage(ctx, addrs[2], ProtocolKad1, req)
	// 	require.ErrorIs(t, err, ErrUnknownPeer)
	// })
}

func TestReqUnknownPeer(t *testing.T) {
	ctx, cancel := kadtest.CtxShort(t)
	defer cancel()

	// create routers
	routers, addrs, ids := createRouters(t, ctx, 3)

	// replace address of peer 1 with an invalid address
	addrs[1] = NewAddrInfo(peer.AddrInfo{
		ID:    addrs[1].PeerID().ID,
		Addrs: []ma.Multiaddr{ma.StringCast("/ip4/1.2.3.4")},
	})
	// "connect" the routers. 0 will store an invalid address for 1, so it
	// won't fail fast, because it believes that it knows how to reach 1
	connectRouters(t, ctx, routers, addrs)

	// verfiy that the wrong address is stored for peer 1 in peer 0
	na, err := routers[0].GetNodeInfo(ctx, ids[1])
	require.NoError(t, err)
	for _, addr := range na.Addresses() {
		require.Contains(t, addrs[1].Addrs, addr)
	}

	req := FindPeerRequest(ids[1])

	_, err = routers[0].SendMessage(ctx, addrs[1], ProtocolKad1, req)
	require.ErrorIs(t, err, swarm.ErrNoGoodAddresses)
}

func TestReqHandlerError(t *testing.T) {
	ctx, cancel := kadtest.CtxShort(t)
	defer cancel()

	routers, addrs, ids := createRouters(t, ctx, 3)
	connectRouters(t, ctx, routers, addrs)

	req := FindPeerRequest(ids[1])

	err := routers[1].AddRequestHandler(ProtocolKad1, &Message{}, func(ctx context.Context,
		id kad.NodeID[key.Key256], req kad.Message,
	) (kad.Message, error) {
		// request handler returns error
		return nil, errors.New("server error")
	})
	require.NoError(t, err)

	_, err = routers[0].SendMessage(ctx, addrs[1], ProtocolKad1, req)
	require.Error(t, err)
}

func TestReqHandlerReturnsWrongType(t *testing.T) {
	// server request handler returns wrong message type, no response is
	// returned by the server
	ctx, cancel := kadtest.CtxShort(t)
	defer cancel()

	routers, addrs, ids := createRouters(t, ctx, 3)
	connectRouters(t, ctx, routers, addrs)

	req := FindPeerRequest(ids[1])

	err := routers[1].AddRequestHandler(ProtocolKad1, &Message{}, func(ctx context.Context,
		id kad.NodeID[key.Key256], req kad.Message,
	) (kad.Message, error) {
		// request handler returns error
		return &sim.Message[key.Key256, ma.Multiaddr]{}, nil
	})
	require.NoError(t, err)

	_, err = routers[0].SendMessage(ctx, addrs[1], ProtocolKad1, req)
	require.Error(t, err)
}
