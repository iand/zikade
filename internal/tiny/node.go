// Package tiny implements Kademlia types suitable for tiny test networks
package tiny

import (
	"fmt"

	"github.com/ipfs/go-libdht/kad"
	"github.com/ipfs/go-libdht/kad/kadtest"
	"github.com/ipfs/go-libdht/kad/key"
)

type Key = kadtest.Key8

type Node struct {
	key Key
}

// Message is a message suitable for tiny test networks.
type Message struct {
	Content string

	// TargetKey is the key the message is directed at.
	TargetKey Key

	// Closer holds the nodes the sender considers closest to the target.
	Closer []Node
}

func (m Message) Target() Key {
	return m.TargetKey
}

func (m Message) CloserNodes() []Node {
	return m.Closer
}

var _ kad.NodeID[Key] = Node{}

func NewNode(k Key) Node {
	return Node{key: k}
}

func (n Node) Key() Key {
	return n.key
}

func (n Node) Equal(other Node) bool {
	return n.key.Compare(other.key) == 0
}

func (n Node) String() string {
	return key.HexString(n.key)
}

// NodeWithCpl returns a [Node] that has a common prefix length of cpl with the supplied [Key]
func NodeWithCpl(k Key, cpl int) (Node, error) {
	if cpl > k.BitLen()-1 {
		return Node{}, fmt.Errorf("cpl too large")
	}

	// flip the bit after the cpl
	mask := Key(1 << (k.BitLen() - cpl - 1))
	return Node{key: k.Xor(mask)}, nil
}
