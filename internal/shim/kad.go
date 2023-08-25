// Package shim provides shims and bridges to go-kademlia types
package shim

import "github.com/plprobelab/go-kademlia/kad"

// ClosestNodesFakeResponse is a shim between query state machine events that expect a message and the newer style that
// doesn't need a message.
func ClosestNodesFakeResponse[K kad.Key[K], A kad.Address[A]](key K, nodes []kad.NodeInfo[K, A]) kad.Response[K, A] {
	return &fakeMessage[K, A]{
		key:   key,
		nodes: nodes,
	}
}

type fakeMessage[K kad.Key[K], A kad.Address[A]] struct {
	key   K
	nodes []kad.NodeInfo[K, A]
}

func (r fakeMessage[K, A]) Target() K {
	return r.key
}

func (r fakeMessage[K, A]) CloserNodes() []kad.NodeInfo[K, A] {
	return r.nodes
}

func (r fakeMessage[K, A]) EmptyResponse() kad.Response[K, A] {
	return &fakeMessage[K, A]{}
}

type NodeAddr[K kad.Key[K], A kad.Address[A]] struct {
	id        kad.NodeID[K]
	addresses []A
}

func NewNodeAddr[K kad.Key[K], A kad.Address[A]](id kad.NodeID[K], addresses []A) *NodeAddr[K, A] {
	return &NodeAddr[K, A]{
		id:        id,
		addresses: addresses,
	}
}

func (n *NodeAddr[K, A]) ID() kad.NodeID[K] {
	return n.id
}

func (n *NodeAddr[K, A]) Addresses() []A {
	return n.addresses
}
