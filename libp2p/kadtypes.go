package libp2p

import (
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	mh "github.com/multiformats/go-multihash"
	mhreg "github.com/multiformats/go-multihash/core"

	"github.com/plprobelab/go-kademlia/kad"
	"github.com/plprobelab/go-kademlia/key"
)

type KadKey = key.Key256

type AddrInfo struct {
	peer.AddrInfo
	id *PeerID
}

var _ kad.NodeInfo[KadKey, multiaddr.Multiaddr] = (*AddrInfo)(nil)

func NewAddrInfo(ai peer.AddrInfo) *AddrInfo {
	return &AddrInfo{
		AddrInfo: ai,
		id:       NewPeerID(ai.ID),
	}
}

func (ai AddrInfo) Key() KadKey {
	return ai.id.Key()
}

func (ai AddrInfo) String() string {
	return ai.id.String()
}

func (ai AddrInfo) PeerID() *PeerID {
	return ai.id
}

func (ai AddrInfo) ID() kad.NodeID[KadKey] {
	return ai.id
}

func (ai AddrInfo) Addresses() []multiaddr.Multiaddr {
	addrs := make([]multiaddr.Multiaddr, len(ai.Addrs))
	copy(addrs, ai.Addrs)
	return addrs
}

type PeerID struct {
	peer.ID
}

var _ kad.NodeID[KadKey] = (*PeerID)(nil)

func NewPeerID(p peer.ID) *PeerID {
	return &PeerID{p}
}

func (id PeerID) Key() KadKey {
	hasher, _ := mhreg.GetHasher(mh.SHA2_256)
	hasher.Write([]byte(id.ID))
	return key.NewKey256(hasher.Sum(nil))
}

func (id PeerID) NodeID() kad.NodeID[KadKey] {
	return &id
}
