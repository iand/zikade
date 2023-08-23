package core

import (
	"context"
	"errors"

	"github.com/plprobelab/go-kademlia/kad"
	"github.com/plprobelab/go-kademlia/key"
)

// Value is a value that may be stored in the DHT.
type Value[K kad.Key[K]] interface {
	Key() K
	MarshalBinary() ([]byte, error)
}

// Node represent a remote node, a participant in the DHT.
type Node[K kad.Key[K], A kad.Address[A]] interface {
	kad.NodeInfo[K, A]

	// GetClosestNodes requests the n closest nodes to the key from the node's local routing table.
	// The node may return fewer nodes than requested.
	GetClosestNodes(ctx context.Context, key K, n int) ([]Node[K, A], error)

	// GetValue requests that the node return any value associated with the supplied key.
	// If the node does not have a value for the key it returns ErrValueNotFound.
	GetValue(ctx context.Context, key K) (Value[K], error)

	// PutValue requests that the node stores a value to be associated with the supplied key.
	// If the node cannot or chooses not to store the value for the key it returns ErrValueNotAccepted.
	PutValue(ctx context.Context, r Value[K], q int) error
}

type DHT[K kad.Key[K], A kad.Address[A]] interface {
	// A DHT supports all Node operations, performing them against its own local routing table and data storage.
	Node[K, A]

	// GetNode retrieves the node associated with the given node id from the DHT's local routing table.
	// If the node isn't found in the table, it returns ErrNodeNotFound.
	GetNode(ctx context.Context, id kad.NodeID[K]) (Node[K, A], error)
}

// FindNodeDHT is a DHT with a FindNode method.
type FindNodeDHT[K kad.Key[K], A kad.Address[A]] interface {
	DHT[K, A]

	// FindNode attempts to find the node associated with the given node id in the DHT. It may read the information from
	// its local routing table, initiate a search in the network, or otherwise locate the information in any way it
	// supports.
	FindNode(ctx context.Context, id kad.NodeID[K]) (Node[K, A], error)
}

// FindClosestDHT is a DHT with a FindClosestNodes method.
type FindClosestDHT[K kad.Key[K], A kad.Address[A]] interface {
	DHT[K, A]

	// FindClosestNodes attempts to find n nodes whose keys are the closest to the target key. It may read the
	// information from its local routing table, initiate a search in the network, or otherwise locate the information
	// in any way it supports.
	FindClosestNodes(ctx context.Context, target K, n int) ([]Node[K, A], error)
}

// ValueDHT is a DHT with FindValue and PlaceValue methods.
type ValueDHT[K kad.Key[K], A kad.Address[A]] interface {
	DHT[K, A]

	// FindValue attempts to locate a value associated with the given key from the DHT's own data store, if any, other
	// nodes in the network or any other means. Any value found is passed to fn. Should fn return the special value
	// SkipFind, FindValue halts and returns nil. If fn yields a non-nil error, FindValue stops completely and returns
	// that error. FindValue stops once it has exhausted possible locations for the value. If no calls to fn were made
	// it returns ErrValueNotFound, otherwise it returns nil.
	FindValue(ctx context.Context, key K, fn FindValueFunc[K, A]) error

	// StoreValue attempts to store a value in the DHT's own data store, if any, and with a quorum of nodes in the
	// network. It returns a list of nodes, other than itself, that it judges may have stored the value, or an error if
	// it encountered a fatal error. In the case that an error is returned the caller must assume that the value may
	// have been stored with any number of nodes in the network.
	StoreValue(ctx context.Context, v Value[K], q int) ([]Node[K, A], error)
}

// FindNode attempts to find the node associated with the given node id in the DHT. If d implements FindNodeDHT, it
// calls d.FindNode. Otherwise, it calls d.GetNode, and if the node is not found, it initiates a query to retrieve the
// information from another node on the network.
func FindNode[K kad.Key[K], A kad.Address[A]](ctx context.Context, d DHT[K, A], id kad.NodeID[K]) (Node[K, A], error) {
	// Defer to the DHT's native FindNode method if it has one.
	if d, ok := d.(FindNodeDHT[K, A]); ok {
		return d.FindNode(ctx, id)
	}

	// See if the DHT already knows the node.
	node, err := d.GetNode(ctx, id)
	if err == nil {
		return node, nil
	}
	if err != nil && !errors.Is(err, ErrNodeNotFound) {
		return nil, err
	}

	// Define the criteria for stopping a query.
	var foundNode Node[K, A]
	fn := func(ctx context.Context, node Node[K, A], stats QueryStats) error {
		if key.Equal(node.ID().Key(), id.Key()) {
			foundNode = node
			return SkipRemaining
		}
		return nil
	}

	// Run a query to find the node, using the DHT's own Query method if it has one.
	_, err = Query(ctx, d, id.Key(), fn)
	if err != nil {
		return nil, err
	}

	// No node was found.
	if foundNode == nil {
		return nil, ErrNodeNotFound
	}

	return foundNode, nil
}

// FindClosestNodes attempts to find n nodes whose keys are the closest to the target key. If d implements FindClosestDHT, it
// calls d.FindClosestNodes. Otherwise, it initiates a query to retrieve the information by traversing the network.
func FindClosestNodes[K kad.Key[K], A kad.Address[A]](ctx context.Context, d DHT[K, A], target K, n int) ([]Node[K, A], error) {
	// Defer to the DHT's native FindClosestNodes method if it has one.
	if d, ok := d.(FindClosestDHT[K, A]); ok {
		return d.FindClosestNodes(ctx, target, n)
	}
	panic("not implemented")
}

// FindValue attempts to find a value corresponding to the given key.
//
// If d implements ValueDHT, it calls d.FindValue. If not, it calls d.GetValue, passing any resulting value to fn.
// Should fn return the special value SkipFind, FindValue halts and returns nil. If fn yields a non-nil error,
// FindValue stops completely and returns that error. Otherwise, a query is initiated to locate other nodes that may
// contain the value. FindValue calls GetValue on every node visited during the query, forwarding any returned value to
// fn, and stops on a non-nil error return, as described for the call to GetValue. If no calls to fn were made
// it returns ErrValueNotFound, otherwise it returns nil.
func FindValue[K kad.Key[K], A kad.Address[A]](ctx context.Context, d DHT[K, A], key K, fn FindValueFunc[K, A]) error {
	if d, ok := d.(ValueDHT[K, A]); ok {
		return d.FindValue(ctx, key, fn)
	}

	valueFound := false
	v, err := d.GetValue(ctx, key)
	if err == nil {
		valueFound = true
		if err := fn(ctx, v, d); err != nil {
			if errors.Is(err, SkipFind) {
				// done
				return nil
			}
			if err != nil {
				// user defined error to terminate the find
				return err
			}
			// fall through to initiate query
		}
	} else if !errors.Is(err, ErrValueNotFound) {
		return err
	}

	// Define the criteria for stopping a query.
	qfn := func(ctx context.Context, node Node[K, A], stats QueryStats) error {
		v, err := node.GetValue(ctx, key)
		if err == nil {
			valueFound = true
			if err := fn(ctx, v, d); err != nil {
				if errors.Is(err, SkipFind) {
					// done
					return nil
				}
				if err != nil {
					// user defined error to terminate the find
					return err
				}
			}
		}
		// ignore error from node
		return nil
	}

	// Run a query to find the value
	_, err = Query(ctx, d, key, qfn)
	if err != nil {
		return err
	}

	// No value was found.
	if !valueFound {
		return ErrValueNotFound
	}

	return nil
}

// FindValueFunc is the type of the function called by FindValue to validate each discovered value.
//
// The error result returned by the function controls how FindValue proceeds. If the function returns the special value
// SkipFind, FindValue skips fetching values from the network and returns nil. Otherwise, if the function returns a
// non-nil error, FindValue stops entirely and returns that error.
//
// The node argument contains the node that the value was retrieved from.
type FindValueFunc[K kad.Key[K], A kad.Address[A]] func(ctx context.Context, v Value[K], node Node[K, A]) error

// SkipFind is used as a return value a FindValueFunc to indicate that all remaining values are to be skipped.
var SkipFind = errors.New("skip find")

// StoreValue attempts to put a value to a quorum of nodes in the network.
//
// If d implements ValueDHT, it calls d.StoreValue. If not, it calls d.PutValue. Should d.PutValue return a non-nil
// error, StoreValue stops and returns that error. Otherwise, it calls FindClosestNodes to locate the q closest nodes
// to the the key associated with the value. StoreValue then calls PutValue on every node returned from
// FindClosestNodes, and it returns a list of nodes that did not yield an error from the PutValue call.
func StoreValue[K kad.Key[K], A kad.Address[A]](ctx context.Context, d DHT[K, A], r Value[K], quorum int) ([]Node[K, A], error) {
	if d, ok := d.(ValueDHT[K, A]); ok {
		return d.StoreValue(ctx, r, quorum)
	}

	panic("not implemented")
}

var (
	ErrNodeNotFound     = errors.New("node not found")
	ErrValueNotFound    = errors.New("value not found")
	ErrValueNotAccepted = errors.New("value not accepted")
)
