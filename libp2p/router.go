package libp2p

import (
	"context"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/multiformats/go-multiaddr"
	"github.com/plprobelab/go-kademlia/kad"
	"github.com/plprobelab/go-kademlia/key"
	"github.com/plprobelab/go-kademlia/network/address"
	"github.com/plprobelab/go-kademlia/network/endpoint"

	"github.com/iand/zikade/kademlia"
)

var ProtocolKad1 = address.ProtocolID("/ipfs/kad/1.0.0")

func NewRouter(ctx context.Context, host host.Host, peerStoreTTL time.Duration) *Router {
	return &Router{
		ctx:          ctx,
		host:         host,
		peerStoreTTL: peerStoreTTL,
	}
}

type Router struct {
	ctx          context.Context
	host         host.Host
	peerStoreTTL time.Duration
}

var _ kademlia.Router[key.Key256, multiaddr.Multiaddr] = (*Router)(nil)

func (r *Router) SendMessage(ctx context.Context, to kad.NodeInfo[key.Key256, multiaddr.Multiaddr], protoID address.ProtocolID, req kad.Request[key.Key256, multiaddr.Multiaddr]) (kad.Response[key.Key256, multiaddr.Multiaddr], error) {
	if protoID != ProtocolKad1 {
		return nil, ErrInvalidProtocol
	}

	if err := r.AddNodeInfo(ctx, to, r.peerStoreTTL); err != nil {
		return nil, fmt.Errorf("add node info: %w", err)
	}

	protoReq, ok := req.(ProtoKadMessage)
	if !ok {
		return nil, ErrRequireProtoKadMessage
	}

	p, ok := to.ID().(*PeerID)
	if !ok {
		return nil, ErrInvalidPeer
	}

	if len(r.host.Peerstore().Addrs(p.ID)) == 0 {
		return nil, ErrUnknownPeer
	}

	var cancel context.CancelFunc
	ctx, cancel = context.WithCancel(ctx)
	defer cancel()

	var err error

	var s network.Stream
	s, err = r.host.NewStream(ctx, p.ID, protocol.ID(protoID))
	if err != nil {
		return nil, fmt.Errorf("stream creation: %w", err)
	}
	defer s.Close()

	err = WriteMsg(s, protoReq)
	if err != nil {
		return nil, fmt.Errorf("write message: %w", err)
	}

	protoResp := new(Message)
	err = ReadMsg(s, protoResp)
	if err != nil {
		return nil, fmt.Errorf("read message: %w", err)
	}

	closer := protoResp.CloserNodes()
	for _, info := range closer {
		r.AddNodeInfo(ctx, info, r.peerStoreTTL)
	}

	return protoResp, err
}

func (r *Router) HandleMessage(ctx context.Context, n kad.NodeID[key.Key256], protoID address.ProtocolID, req kad.Request[key.Key256, multiaddr.Multiaddr]) (kad.Response[key.Key256, multiaddr.Multiaddr], error) {
	panic("not implemented")
}

func (r *Router) AddNodeInfo(ctx context.Context, info kad.NodeInfo[key.Key256, multiaddr.Multiaddr], ttl time.Duration) error {
	p, ok := info.ID().(*PeerID)
	if !ok {
		return ErrInvalidPeer
	}

	ai := peer.AddrInfo{
		ID:    p.ID,
		Addrs: info.Addresses(),
	}

	// Don't add addresses for self or our connected peers. We have better ones.
	if ai.ID == r.host.ID() ||
		r.host.Network().Connectedness(ai.ID) == network.Connected {
		return nil
	}
	r.host.Peerstore().AddAddrs(ai.ID, ai.Addrs, ttl)
	return nil
}

func (r *Router) GetNodeInfo(ctx context.Context, id kad.NodeID[key.Key256]) (kad.NodeInfo[key.Key256, multiaddr.Multiaddr], error) {
	p, ok := id.(*PeerID)
	if !ok {
		return nil, ErrInvalidPeer
	}

	ai, err := r.PeerInfo(p)
	if err != nil {
		return nil, err
	}
	return NewAddrInfo(ai), nil
}

func (r *Router) GetClosestNodes(ctx context.Context, to kad.NodeInfo[key.Key256, multiaddr.Multiaddr], target key.Key256) ([]kad.NodeInfo[key.Key256, multiaddr.Multiaddr], error) {
	resp, err := r.SendMessage(ctx, to, ProtocolKad1, FindKeyRequest(target))
	if err != nil {
		return nil, err
	}
	return resp.CloserNodes(), nil
}

func (r *Router) PeerInfo(id *PeerID) (peer.AddrInfo, error) {
	p, err := getPeerID(id)
	if err != nil {
		return peer.AddrInfo{}, err
	}
	return r.host.Peerstore().PeerInfo(p.ID), nil
}

func (r *Router) Connectedness(id *PeerID) (endpoint.Connectedness, error) {
	p, err := getPeerID(id)
	if err != nil {
		return endpoint.NotConnected, err
	}

	c := r.host.Network().Connectedness(p.ID)
	switch c {
	case network.NotConnected:
		return endpoint.NotConnected, nil
	case network.Connected:
		return endpoint.Connected, nil
	case network.CanConnect:
		return endpoint.CanConnect, nil
	case network.CannotConnect:
		return endpoint.CannotConnect, nil
	default:
		panic(fmt.Sprintf("unexpected libp2p connectedness value: %v", c))
	}
}

func (r *Router) DialPeer(ctx context.Context, id *PeerID) error {
	p, err := getPeerID(id)
	if err != nil {
		return err
	}

	if r.host.Network().Connectedness(p.ID) == network.Connected {
		return nil
	}

	pi := peer.AddrInfo{ID: p.ID}
	if err := r.host.Connect(ctx, pi); err != nil {
		return err
	}
	return nil
}

func (e *Router) Key() key.Key256 {
	return PeerID{ID: e.host.ID()}.Key()
}
