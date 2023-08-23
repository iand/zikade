package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/plprobelab/go-kademlia/kad"
	"github.com/plprobelab/go-kademlia/key"
	"github.com/plprobelab/go-kademlia/network/address"
	"github.com/plprobelab/go-kademlia/query"
)

// QueryDHT is a DHT with a Query method.
type QueryDHT[K kad.Key[K], A kad.Address[A]] interface {
	DHT[K, A]

	// Query traverses the DHT calling fn for each node visited.
	Query(ctx context.Context, target K, fn QueryFunc[K, A]) (QueryStats, error)
}

// Query traverses the DHT calling fn for each node visited.
//
// The query maintains a list of nodes ordered by distance from target and at each step calls fn for the node closest to
// the head of the list that has not already been visited. If fn returns a non-nil error Query progresses by calling
// GetClosestNodes on the node and adding the results to its node list. If fn returns SkipNode then Query will not call
// GetClosestNodes but will proceed with visiting the next nearest node on the list. Query terminates without error
// when all nodes have been visited or fn returns SkipRemaining. Otherwise, if fn returns a non-nil error, Query stops
// entirely and returns that error.
func Query[K kad.Key[K], A kad.Address[A]](ctx context.Context, d DHT[K, A], target K, fn QueryFunc[K, A]) (QueryStats, error) {
	if d, ok := d.(QueryDHT[K, A]); ok {
		return d.Query(ctx, target, fn)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	seeds, err := d.GetClosestNodes(ctx, target, 20)
	if err != nil {
		return QueryStats{}, err
	}

	nodeIdx := make(map[string]Node[K, A])
	knownClosestNodes := make([]kad.NodeID[K], 0, len(seeds))
	for _, s := range seeds {
		idxKey := key.HexString(s.ID().Key())
		if _, ok := nodeIdx[idxKey]; ok {
			continue
		}
		knownClosestNodes = append(knownClosestNodes, s.ID())
		nodeIdx[idxKey] = s
	}

	var protocolID address.ProtocolID
	var req kad.Request[K, A]
	iter := query.NewClosestNodesIter(target)

	q, err := query.NewQuery[K, A](d.ID(), "foo", protocolID, req, iter, knownClosestNodes, nil)
	if err != nil {
		return QueryStats{}, err
	}

	var lastStats QueryStats
	pending := make(chan query.QueryEvent, 1)
	pending <- nil
	for {
		select {
		case <-ctx.Done():
			return lastStats, ctx.Err()
		case ev := <-pending:
			st := q.Advance(ctx, ev)
			switch st := st.(type) {
			case *query.StateQueryWaitingMessage[K, A]:
				lastStats = QueryStats{
					Start:    st.Stats.Start,
					Requests: st.Stats.Requests,
					Success:  st.Stats.Success,
					Failure:  st.Stats.Failure,
				}

				idxKey := key.HexString(st.NodeID.Key())
				node, ok := nodeIdx[idxKey]
				if !ok {
					return lastStats, fmt.Errorf("query state machine requested unknown node: %v", st.NodeID)
				}
				err := fn(ctx, node, lastStats)
				if errors.Is(err, SkipRemaining) {
					// done
					cancel()
					return lastStats, nil
				}
				if errors.Is(err, SkipNode) {
					// done
					break
				}
				if err != nil {
					// user defined error that terminates the query
					return lastStats, err
				}

				go func() {
					closer, err := node.GetClosestNodes(ctx, target, 20)
					if err != nil {
						select {
						case <-ctx.Done():
						case pending <- &query.EventQueryMessageFailure[K]{
							NodeID: node.ID(),
							Error:  err,
						}:
						}
						return
					}

					closerInfos := make([]kad.NodeInfo[K, A], 0, len(seeds))
					for _, c := range closer {
						idxKey := key.HexString(c.ID().Key())
						if _, ok := nodeIdx[idxKey]; ok {
							continue
						}
						closerInfos = append(closerInfos, c)
						nodeIdx[idxKey] = c
					}

					select {
					case <-ctx.Done():
						return
					case pending <- &query.EventQueryMessageResponse[K, A]{
						NodeID:   node.ID(),
						Response: ClosestNodesFakeResponse(target, closerInfos),
					}:
					}
				}()
			case *query.StateQueryFinished:
				// done
				lastStats.Exhausted = true
				return lastStats, nil
			case *query.StateQueryWaitingAtCapacity:
			case *query.StateQueryWaitingWithCapacity:
			default:
				panic(fmt.Sprintf("unexpected query state: %T", st))
			}
		}
	}
}

// QueryFunc is the type of the function called by Query to visit each node.
//
// The error result returned by the function controls how Query proceeds. If the function returns the special value
// SkipNode, Query skips fetching closer nodes from the current node. If the function returns the special value
// SkipRemaining, Query skips all visiting all remaining nodes. Otherwise, if the function returns a non-nil error,
// Query stops entirely and returns that error.
//
// The stats argument contains statistics on the progress of the query so far.
type QueryFunc[K kad.Key[K], A kad.Address[A]] func(ctx context.Context, node Node[K, A], stats QueryStats) error

type QueryStats struct {
	Start     time.Time // Start is the time the query began executing.
	End       time.Time // End is the time the query stopped executing.
	Requests  int       // Requests is a count of the number of requests made by the query.
	Success   int       // Success is a count of the number of nodes the query succesfully contacted.
	Failure   int       // Failure is a count of the number of nodes the query received an error response from.
	Exhausted bool      // Exhausted is true if the query ended after visiting every node it could.
}

var (
	// SkipNode is used as a return value from a QueryFunc to indicate that the node is to be skipped.
	SkipNode = errors.New("skip node")

	// SkipRemaining is used as a return value a QueryFunc to indicate that all remaining nodes are to be skipped.
	SkipRemaining = errors.New("skip remaining nodes")
)

// ClosestNodesFakeResponse is a shim between query state machine events that expect a message and the newer style
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
