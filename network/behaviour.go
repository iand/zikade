package network

import (
	"context"
	"fmt"
	"sync"

	"github.com/plprobelab/go-kademlia/kad"
	"github.com/plprobelab/go-kademlia/key"
	"github.com/plprobelab/go-kademlia/query"
	"golang.org/x/exp/slog"

	"github.com/iand/zikade/coord"
	"github.com/iand/zikade/core"
)

type NetworkBehaviour[K kad.Key[K], A kad.Address[A]] struct {
	// rtr is the message router used to send messages
	rtr Router[K, A]

	nodeHandlersMu sync.Mutex
	nodeHandlers   map[string]*NodeHandler[K, A] // TODO: garbage collect node handlers

	pendingMu sync.Mutex
	pending   []coord.DhtEvent
	ready     chan struct{}

	cfg Config
}

func NewNetworkBehaviour[K kad.Key[K], A kad.Address[A]](rtr Router[K, A], cfg *Config) (*NetworkBehaviour[K, A], error) {
	b := &NetworkBehaviour[K, A]{
		rtr:          rtr,
		nodeHandlers: make(map[string]*NodeHandler[K, A]),
		ready:        make(chan struct{}, 1),
		cfg:          *cfg,
	}

	return b, nil
}

func (b *NetworkBehaviour[K, A]) Notify(ctx context.Context, ev coord.DhtEvent) {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()

	switch ev := ev.(type) {
	case *coord.EventOutboundGetClosestNodes[K, A]:
		nodeKey := key.HexString(ev.To.ID().Key())
		b.nodeHandlersMu.Lock()
		nh, ok := b.nodeHandlers[nodeKey]
		if !ok {
			nh = NewNodeHandler(ev.To, b.rtr, b.cfg.Logger)
			b.nodeHandlers[nodeKey] = nh
		}
		b.nodeHandlersMu.Unlock()
		nh.Notify(ctx, ev)
	default:
		panic(fmt.Sprintf("unexpected dht event: %T", ev))
	}

	if len(b.pending) > 0 {
		select {
		case b.ready <- struct{}{}:
		default:
		}
	}
}

func (b *NetworkBehaviour[K, A]) Ready() <-chan struct{} {
	return b.ready
}

func (b *NetworkBehaviour[K, A]) Perform(ctx context.Context) (coord.DhtEvent, bool) {
	// No inbound work can be done until Perform is complete
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()

	// drain queued events.
	if len(b.pending) > 0 {
		var ev coord.DhtEvent
		ev, b.pending = b.pending[0], b.pending[1:]

		if len(b.pending) > 0 {
			select {
			case b.ready <- struct{}{}:
			default:
			}
		}
		return ev, true
	}

	return nil, false
}

func (b *NetworkBehaviour[K, A]) getNodeHandler(ctx context.Context, id kad.NodeID[K]) (*NodeHandler[K, A], error) {
	nodeKey := key.HexString(id.Key())
	b.nodeHandlersMu.Lock()
	nh, ok := b.nodeHandlers[nodeKey]
	if !ok {
		info, err := b.rtr.GetNodeInfo(ctx, id)
		if err != nil {
			return nil, err
		}
		nh = NewNodeHandler(info, b.rtr, b.cfg.Logger)
		b.nodeHandlers[nodeKey] = nh
	}
	b.nodeHandlersMu.Unlock()
	return nh, nil
}

type NodeHandler[K kad.Key[K], A kad.Address[A]] struct {
	self   kad.NodeInfo[K, A]
	rtr    Router[K, A]
	queue  *coord.WorkQueue[coord.NodeHandlerRequest]
	logger *slog.Logger
}

func NewNodeHandler[K kad.Key[K], A kad.Address[A]](self kad.NodeInfo[K, A], rtr Router[K, A], logger *slog.Logger) *NodeHandler[K, A] {
	h := &NodeHandler[K, A]{
		self:   self,
		rtr:    rtr,
		logger: logger,
	}

	h.queue = coord.NewWorkQueue(h.send)

	return h
}

func (h *NodeHandler[K, A]) Notify(ctx context.Context, ev coord.NodeHandlerRequest) {
	h.queue.Enqueue(ctx, ev)
}

func (h *NodeHandler[K, A]) send(ctx context.Context, ev coord.NodeHandlerRequest) bool {
	switch cmd := ev.(type) {
	case *coord.EventOutboundGetClosestNodes[K, A]:
		if cmd.Notify == nil {
			break
		}
		nodes, err := h.rtr.GetClosestNodes(ctx, h.self, cmd.Target)
		if err != nil {
			cmd.Notify.Notify(ctx, &coord.EventGetClosestNodesFailure[K, A]{
				QueryID: cmd.QueryID,
				To:      h.self,
				Target:  cmd.Target,
				Err:     fmt.Errorf("send: %w", err),
			})
			return false
		}

		cmd.Notify.Notify(ctx, &coord.EventGetClosestNodesSuccess[K, A]{
			QueryID:      cmd.QueryID,
			To:           h.self,
			Target:       cmd.Target,
			ClosestNodes: nodes,
		})
	default:
		panic(fmt.Sprintf("unexpected command type: %T", cmd))
	}

	return false
}

func (h *NodeHandler[K, A]) ID() kad.NodeID[K] {
	return h.self.ID()
}

func (h *NodeHandler[K, A]) Addresses() []A {
	return h.self.Addresses()
}

// GetClosestNodes requests the n closest nodes to the key from the node's local routing table.
// The node may return fewer nodes than requested.
func (h *NodeHandler[K, A]) GetClosestNodes(ctx context.Context, k K, n int) ([]core.Node[K, A], error) {
	w := coord.NewWaiter[coord.DhtEvent]()

	ev := &coord.EventOutboundGetClosestNodes[K, A]{
		QueryID: query.QueryID(key.HexString(k)),
		To:      h.self,
		Target:  k,
		Notify:  w,
	}

	h.queue.Enqueue(ctx, ev)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case we := <-w.Chan():

		switch res := we.Event.(type) {
		case *coord.EventGetClosestNodesSuccess[K, A]:
			nodes := make([]core.Node[K, A], 0, len(res.ClosestNodes))
			for _, info := range res.ClosestNodes {
				// TODO use a global registry of node handlers
				nodes = append(nodes, NewNodeHandler(info, h.rtr, h.logger))
				n--
				if n == 0 {
					break
				}
			}
			return nodes, nil

		case *coord.EventGetClosestNodesFailure[K, A]:
			return nil, res.Err
		default:
			panic(fmt.Sprintf("unexpected node handler event: %T", ev))
		}
	}
}

// GetValue requests that the node return any value associated with the supplied key.
// If the node does not have a value for the key it returns ErrValueNotFound.
func (h *NodeHandler[K, A]) GetValue(ctx context.Context, key K) (core.Value[K], error) {
	panic("not implemented")
}

// PutValue requests that the node stores a value to be associated with the supplied key.
// If the node cannot or chooses not to store the value for the key it returns ErrValueNotAccepted.
func (h *NodeHandler[K, A]) PutValue(ctx context.Context, r core.Value[K], q int) error {
	panic("not implemented")
}
