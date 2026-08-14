package coord

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/probe-lab/zikade/errs"
	"github.com/probe-lab/zikade/internal/coord/coordt"
	"github.com/probe-lab/zikade/kadt"
	"github.com/probe-lab/zikade/pb"
	"github.com/probe-lab/zikade/tele"
)

// ErrRequestDropped is the error reported for a request dropped because no capacity was
// available for it, either for the peer it was addressed to or across all peers.
var ErrRequestDropped = errors.New("request dropped")

type NetworkConfig struct {
	// Logger is a structured logger that will be used when logging.
	Logger *slog.Logger

	// Tracer is the tracer that should be used to trace execution.
	Tracer trace.Tracer

	// Meter is the meter that should be used to record metrics.
	Meter metric.Meter

	// Capacity is the maximum number of requests that may be queued or in flight across
	// all peers.
	Capacity int

	// PeerCapacity is the maximum number of requests that may be queued or in flight for
	// any one peer.
	PeerCapacity int
}

// Validate checks the configuration options and returns an error if any have invalid values.
func (cfg *NetworkConfig) Validate() error {
	if cfg.Logger == nil {
		return &errs.ConfigurationError{
			Component: "NetworkConfig",
			Err:       fmt.Errorf("logger must not be nil"),
		}
	}

	if cfg.Tracer == nil {
		return &errs.ConfigurationError{
			Component: "NetworkConfig",
			Err:       fmt.Errorf("tracer must not be nil"),
		}
	}

	if cfg.Meter == nil {
		return &errs.ConfigurationError{
			Component: "NetworkConfig",
			Err:       fmt.Errorf("meter must not be nil"),
		}
	}

	if cfg.Capacity < 1 {
		return &errs.ConfigurationError{
			Component: "NetworkConfig",
			Err:       fmt.Errorf("capacity must be greater than zero"),
		}
	}

	if cfg.PeerCapacity < 1 {
		return &errs.ConfigurationError{
			Component: "NetworkConfig",
			Err:       fmt.Errorf("peer capacity must be greater than zero"),
		}
	}

	return nil
}

func DefaultNetworkConfig() *NetworkConfig {
	return &NetworkConfig{
		Logger:       tele.DefaultLogger("coord"),
		Tracer:       tele.NoopTracer(),
		Meter:        tele.NoopMeter(),
		Capacity:     256, // MAGIC
		PeerCapacity: 3,   // MAGIC
	}
}

type NetworkBehaviour struct {
	// cfg is a copy of the optional configuration supplied to the behaviour
	cfg NetworkConfig

	// rtr is the message router used to send messages
	rtr coordt.Router[kadt.Key, kadt.PeerID, *pb.Message]

	nodeHandlersMu sync.Mutex
	nodeHandlers   map[kadt.PeerID]*NodeHandler // TODO: garbage collect node handlers

	// slots limits the number of requests queued or in flight across all node handlers
	slots *slots

	// counterRequestsDropped tracks the number of requests dropped due to no available
	// capacity.
	counterRequestsDropped metric.Int64Counter

	pendingMu sync.Mutex
	pending   []BehaviourEvent
	ready     chan struct{}

	logger *slog.Logger
	tracer trace.Tracer
}

func NewNetworkBehaviour(rtr coordt.Router[kadt.Key, kadt.PeerID, *pb.Message], cfg *NetworkConfig) (*NetworkBehaviour, error) {
	if cfg == nil {
		cfg = DefaultNetworkConfig()
	} else if err := cfg.Validate(); err != nil {
		return nil, err
	}

	b := &NetworkBehaviour{
		cfg:          *cfg,
		rtr:          rtr,
		nodeHandlers: make(map[kadt.PeerID]*NodeHandler),
		slots:        newSlots(cfg.Capacity),
		ready:        make(chan struct{}, 1),
		logger:       cfg.Logger.With("behaviour", "network"),
		tracer:       cfg.Tracer,
	}

	var err error
	b.counterRequestsDropped, err = cfg.Meter.Int64Counter(
		"network_requests_dropped",
		metric.WithDescription("Total number of requests dropped due to no available capacity"),
	)
	if err != nil {
		return nil, fmt.Errorf("create network_requests_dropped counter: %w", err)
	}

	return b, nil
}

// slots is a counting semaphore that never blocks a caller.
type slots struct {
	tokens chan struct{}
}

func newSlots(n int) *slots {
	return &slots{tokens: make(chan struct{}, n)}
}

// acquire takes a slot, reporting whether one was free.
func (s *slots) acquire() bool {
	select {
	case s.tokens <- struct{}{}:
		return true
	default:
		return false
	}
}

// release returns a slot taken by a previous call to acquire. It does not block when no
// slots are held, so an unbalanced release cannot stop the caller.
func (s *slots) release() {
	select {
	case <-s.tokens:
	default:
	}
}

// Notify hands a request to the node handler for the peer it is addressed to. It does not
// block: a request that finds no available capacity is dropped and reported back to whoever
// asked for it as an ordinary failure, since blocking here would stop the event loop that
// is the only thing able to drain the handler.
func (b *NetworkBehaviour) Notify(ctx context.Context, ev BehaviourEvent) {
	ctx, span := b.tracer.Start(ctx, "NetworkBehaviour.Notify")
	defer span.End()

	switch ev := ev.(type) {
	case *EventOutboundGetCloserNodes:
		if !b.handler(ev.To).Notify(ctx, ev) {
			b.counterRequestsDropped.Add(ctx, 1)
			b.logger.Debug("dropped request to find closer nodes", tele.LogAttrPeerID(ev.To))
			if ev.Notify != nil {
				ev.Notify.Notify(ctx, &EventGetCloserNodesFailure{
					QueryID: ev.QueryID,
					To:      ev.To,
					Target:  ev.Target,
					Err:     ErrRequestDropped,
				})
			}
		}
	case *EventOutboundSendMessage:
		if !b.handler(ev.To).Notify(ctx, ev) {
			b.counterRequestsDropped.Add(ctx, 1)
			b.logger.Debug("dropped request to send message", tele.LogAttrPeerID(ev.To))
			if ev.Notify != nil {
				ev.Notify.Notify(ctx, &EventSendMessageFailure{
					QueryID: ev.QueryID,
					To:      ev.To,
					Request: ev.Message,
					Err:     ErrRequestDropped,
				})
			}
		}
	default:
		panic(fmt.Sprintf("unexpected dht event: %T", ev))
	}

	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()

	if len(b.pending) > 0 {
		select {
		case b.ready <- struct{}{}:
		default:
		}
	}
}

// handler returns the node handler for a peer, creating it if there is not one already.
func (b *NetworkBehaviour) handler(to kadt.PeerID) *NodeHandler {
	b.nodeHandlersMu.Lock()
	defer b.nodeHandlersMu.Unlock()

	nh, ok := b.nodeHandlers[to]
	if !ok {
		nh = NewNodeHandler(to, b.rtr, b.slots, b.cfg.PeerCapacity, b.logger, b.tracer)
		b.nodeHandlers[to] = nh
	}
	return nh
}

func (b *NetworkBehaviour) Ready() <-chan struct{} {
	return b.ready
}

func (b *NetworkBehaviour) Perform(ctx context.Context) (BehaviourEvent, bool) {
	_, span := b.tracer.Start(ctx, "NetworkBehaviour.Perform")
	defer span.End()
	// No inbound work can be done until Perform is complete
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()

	// drain queued events.
	if len(b.pending) > 0 {
		var ev BehaviourEvent
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

// Close stops all the node handlers managed by the behaviour, releasing the
// goroutines they use to send messages. It is safe to call Close more than once.
func (b *NetworkBehaviour) Close() {
	b.nodeHandlersMu.Lock()
	defer b.nodeHandlersMu.Unlock()

	for _, nh := range b.nodeHandlers {
		nh.Close()
	}
	clear(b.nodeHandlers)
}

// A NodeHandler sends requests to a single peer, one at a time, from a goroutine of its
// own. Requests that arrive with no available capacity are dropped rather than queued.
type NodeHandler struct {
	self   kadt.PeerID
	rtr    coordt.Router[kadt.Key, kadt.PeerID, *pb.Message]
	logger *slog.Logger
	tracer trace.Tracer

	// slots is the capacity shared with every other node handler, and peerSlots the
	// capacity of this handler alone. A request holds one of each from the moment it is
	// accepted until it has been sent or discarded.
	slots     *slots
	peerSlots *slots

	// pending holds the accepted requests that have not been sent yet
	pending chan CtxEvent[NodeHandlerRequest]

	stop     chan struct{}
	stopOnce sync.Once
}

func NewNodeHandler(self kadt.PeerID, rtr coordt.Router[kadt.Key, kadt.PeerID, *pb.Message], slots *slots, capacity int, logger *slog.Logger, tracer trace.Tracer) *NodeHandler {
	h := &NodeHandler{
		self:      self,
		rtr:       rtr,
		logger:    logger,
		tracer:    tracer,
		slots:     slots,
		peerSlots: newSlots(capacity),
		pending:   make(chan CtxEvent[NodeHandlerRequest], capacity),
		stop:      make(chan struct{}),
	}

	go h.run()

	return h
}

// run sends the accepted requests in turn until the handler is closed.
func (h *NodeHandler) run() {
	for {
		select {
		case <-h.stop:
			return
		case ce := <-h.pending:
			if ce.Ctx.Err() == nil {
				h.send(ce.Ctx, ce.Event)
			}
			h.release()
		}
	}
}

// Notify accepts a request to be sent to the handler's peer, reporting whether capacity was
// available for it. It does not block.
func (h *NodeHandler) Notify(ctx context.Context, ev NodeHandlerRequest) bool {
	ctx, span := h.tracer.Start(ctx, "NodeHandler.Notify")
	defer span.End()

	if !h.slots.acquire() {
		return false
	}
	if !h.peerSlots.acquire() {
		h.slots.release()
		return false
	}

	// the send cannot block: this handler holds no more slots than its queue has room for
	h.pending <- CtxEvent[NodeHandlerRequest]{Ctx: ctx, Event: ev}
	return true
}

// release returns the capacity held by a request that has been sent or discarded.
func (h *NodeHandler) release() {
	h.peerSlots.release()
	h.slots.release()
}

func (h *NodeHandler) send(ctx context.Context, ev NodeHandlerRequest) {
	switch cmd := ev.(type) {
	case *EventOutboundGetCloserNodes:
		if cmd.Notify == nil {
			break
		}
		nodes, err := h.rtr.GetClosestNodes(ctx, h.self, cmd.Target)
		if err != nil {
			cmd.Notify.Notify(ctx, &EventGetCloserNodesFailure{
				QueryID: cmd.QueryID,
				To:      h.self,
				Target:  cmd.Target,
				Err:     fmt.Errorf("NodeHandler: %w", err),
			})
			return
		}

		cmd.Notify.Notify(ctx, &EventGetCloserNodesSuccess{
			QueryID:     cmd.QueryID,
			To:          h.self,
			Target:      cmd.Target,
			CloserNodes: nodes,
		})
	case *EventOutboundSendMessage:
		if cmd.Notify == nil {
			break
		}
		resp, err := h.rtr.SendMessage(ctx, h.self, cmd.Message)
		if err != nil {
			cmd.Notify.Notify(ctx, &EventSendMessageFailure{
				QueryID: cmd.QueryID,
				To:      h.self,
				Request: cmd.Message,
				Err:     fmt.Errorf("NodeHandler: %w", err),
			})
			return
		}

		cmd.Notify.Notify(ctx, &EventSendMessageSuccess{
			QueryID:     cmd.QueryID,
			To:          h.self,
			Request:     cmd.Message,
			Response:    resp,
			CloserNodes: resp.CloserNodes(),
		})
	default:
		panic(fmt.Sprintf("unexpected command type: %T", cmd))
	}
}

// Close stops the handler from sending any further requests and discards the requests it
// has accepted but not yet sent. It is safe to call Close more than once.
func (h *NodeHandler) Close() {
	h.stopOnce.Do(func() {
		close(h.stop)

		// release the capacity held by the requests that will now never be sent
		for {
			select {
			case <-h.pending:
				h.release()
			default:
				return
			}
		}
	})
}

func (h *NodeHandler) ID() kadt.PeerID {
	return h.self
}
