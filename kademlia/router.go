package kademlia

import (
	"context"
	"time"

	"github.com/plprobelab/go-kademlia/kad"
	"github.com/plprobelab/go-kademlia/network/address"
)

// Router its a work in progress

type Router[K kad.Key[K], A kad.Address[A]] interface {
	// SendMessage attempts to send a request to another node. The Router will absorb the addresses in to into its
	// internal nodestore. This method blocks until a response is received or an error is encountered.
	SendMessage(ctx context.Context, to kad.NodeInfo[K, A], protoID address.ProtocolID, req kad.Request[K, A]) (kad.Response[K, A], error)

	AddNodeInfo(ctx context.Context, info kad.NodeInfo[K, A], ttl time.Duration) error
	GetNodeInfo(ctx context.Context, id kad.NodeID[K]) (kad.NodeInfo[K, A], error)

	// GetClosestNodes attempts to send a request to another node asking it for nodes that it considers to be
	// closest to the target key.
	GetClosestNodes(ctx context.Context, to kad.NodeInfo[K, A], target K) ([]kad.NodeInfo[K, A], error)
}
