package libp2p

import (
	"context"
	"errors"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-msgio/pbio"
	"github.com/multiformats/go-multiaddr"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"

	"github.com/plprobelab/go-kademlia/kad"
	"github.com/plprobelab/go-kademlia/key"
)

var ErrNoValidAddresses = errors.New("no valid addresses")

func FindPeerRequest(p *PeerID) *Message {
	marshalledPeerid, _ := p.MarshalBinary()
	return &Message{
		Type: Message_FIND_NODE,
		Key:  marshalledPeerid,
	}
}

func FindKeyRequest(k key.Key256) *Message {
	marshalledKey, _ := k.MarshalBinary()
	return &Message{
		Type: Message_FIND_NODE,
		Key:  marshalledKey,
	}
}

func FindPeerResponse(ctx context.Context, peers []kad.NodeID[key.Key256], r *Router) *Message {
	return &Message{
		Type:        Message_FIND_NODE,
		CloserPeers: NodeIDsToPbPeers(ctx, peers, r),
	}
}

func WriteMsg(s network.Stream, msg protoreflect.ProtoMessage) error {
	w := pbio.NewDelimitedWriter(s)
	return w.WriteMsg(msg)
}

func ReadMsg(s network.Stream, msg protoreflect.ProtoMessage) error {
	r := pbio.NewDelimitedReader(s, network.MessageSizeMax)
	return r.ReadMsg(msg)
}

func (msg *Message) Target() key.Key256 {
	p, err := peer.IDFromBytes(msg.GetKey())
	if err != nil {
		return key.ZeroKey256()
	}
	return PeerID{ID: p}.Key()
}

func (msg *Message) Protocol() string {
	return "/test/1.0.0"
}

func (msg *Message) EmptyResponse() kad.Response[key.Key256, multiaddr.Multiaddr] {
	return &Message{}
}

func (msg *Message) CloserNodes() []kad.NodeInfo[key.Key256, multiaddr.Multiaddr] {
	closerPeers := msg.GetCloserPeers()
	if closerPeers == nil {
		return []kad.NodeInfo[key.Key256, multiaddr.Multiaddr]{}
	}
	return ParsePeers(closerPeers)
}

func PBPeerToPeerInfo(pbp *Message_Peer) (*AddrInfo, error) {
	addrs := make([]multiaddr.Multiaddr, 0, len(pbp.Addrs))
	for _, a := range pbp.Addrs {
		addr, err := multiaddr.NewMultiaddrBytes(a)
		if err == nil {
			addrs = append(addrs, addr)
		}
	}
	if len(addrs) == 0 {
		return nil, ErrNoValidAddresses
	}

	return NewAddrInfo(peer.AddrInfo{
		ID:    peer.ID(pbp.Id),
		Addrs: addrs,
	}), nil
}

func ParsePeers(pbps []*Message_Peer) []kad.NodeInfo[key.Key256, multiaddr.Multiaddr] {
	peers := make([]kad.NodeInfo[key.Key256, multiaddr.Multiaddr], 0, len(pbps))
	for _, p := range pbps {
		pi, err := PBPeerToPeerInfo(p)
		if err == nil {
			peers = append(peers, pi)
		}
	}
	return peers
}

func NodeIDsToPbPeers(ctx context.Context, peers []kad.NodeID[key.Key256], r *Router) []*Message_Peer {
	if len(peers) == 0 || r == nil {
		return nil
	}

	pbPeers := make([]*Message_Peer, 0, len(peers))
	for _, n := range peers {
		p := n.(*PeerID)

		id, err := r.GetNodeInfo(ctx, n)
		if err != nil {
			continue
		}
		// convert NetworkAddress to []multiaddr.Multiaddr
		addrs := id.(*AddrInfo).Addrs
		pbAddrs := make([][]byte, len(addrs))
		// convert multiaddresses to bytes
		for i, a := range addrs {
			pbAddrs[i] = a.Bytes()
		}

		connectedness, err := r.Connectedness(ctx, p)
		if err != nil {
			continue
		}
		pbPeers = append(pbPeers, &Message_Peer{
			Id:         []byte(p.ID),
			Addrs:      pbAddrs,
			Connection: Message_ConnectionType(connectedness),
		})
	}
	return pbPeers
}
