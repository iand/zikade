package coord

// TODO: port these methods to dht package

// import (
// 	"context"
// 	"errors"
// 	"fmt"
// 	"time"

// 	"github.com/benbjohnson/clock"
// 	logging "github.com/ipfs/go-log/v2"
// 	"github.com/plprobelab/go-kademlia/kad"
// 	"github.com/plprobelab/go-kademlia/kaderr"
// 	"github.com/plprobelab/go-kademlia/key"
// 	"github.com/plprobelab/go-kademlia/network/address"
// 	"github.com/plprobelab/go-kademlia/query"
// 	"github.com/plprobelab/go-kademlia/routing"
// 	"github.com/plprobelab/go-kademlia/util"
// 	"go.uber.org/zap/exp/zapslog"
// 	"golang.org/x/exp/slog"

// 	"github.com/iand/zikade/core"
// )

// // RoutingNotifications returns a channel that may be read to be notified of routing updates
// func (d *Dht[K, A]) RoutingNotifications() <-chan RoutingNotification {
// 	return d.routingNotifications
// }

// func (d *Dht[K, A]) eventLoop() {
// 	ctx := context.Background()

// 	for {
// 		var ev DhtEvent
// 		var ok bool
// 		select {
// 		case <-d.networkBehaviour.Ready():
// 			ev, ok = d.networkBehaviour.Perform(ctx)
// 		case <-d.routingBehaviour.Ready():
// 			ev, ok = d.routingBehaviour.Perform(ctx)
// 		case <-d.queryBehaviour.Ready():
// 			ev, ok = d.queryBehaviour.Perform(ctx)
// 		}

// 		if ok {
// 			d.dispatchDhtNotice(ctx, ev)
// 		}
// 	}
// }

// func (c *Dht[K, A]) dispatchDhtNotice(ctx context.Context, ev DhtEvent) {
// 	ctx, span := util.StartSpan(ctx, "Dht.dispatchDhtNotice")
// 	defer span.End()

// 	switch ev := ev.(type) {
// 	case *EventDhtStartBootstrap[K, A]:
// 		c.routingBehaviour.Notify(ctx, ev)
// 	case *EventOutboundGetClosestNodes[K, A]:
// 		c.networkBehaviour.Notify(ctx, ev)
// 	case *EventStartQuery[K, A]:
// 		c.queryBehaviour.Notify(ctx, ev)
// 	case *EventStopQuery:
// 		c.queryBehaviour.Notify(ctx, ev)
// 	case *EventDhtAddNodeInfo[K, A]:
// 		c.routingBehaviour.Notify(ctx, ev)
// 	case RoutingNotification:
// 		select {
// 		case <-ctx.Done():
// 		case c.routingNotifications <- ev:
// 		default:
// 		}
// 	}
// }

// // GetNode retrieves the node associated with the given node id from the DHT's local routing table.
// // If the node isn't found in the table, it returns core.ErrNodeNotFound.
// func (d *Dht[K, A]) GetNode(ctx context.Context, id kad.NodeID[K]) (core.Node[K, A], error) {
// 	if _, exists := d.rt.GetNode(id.Key()); !exists {
// 		return nil, core.ErrNodeNotFound
// 	}

// 	nh, err := d.networkBehaviour.getNodeHandler(ctx, id)
// 	if err != nil {
// 		return nil, err
// 	}
// 	return nh, nil
// }

// // GetClosestNodes requests the n closest nodes to the key from the node's local routing table.
// func (d *Dht[K, A]) GetClosestNodes(ctx context.Context, k K, n int) ([]core.Node[K, A], error) {
// 	closest := d.rt.NearestNodes(k, n)
// 	nodes := make([]core.Node[K, A], 0, len(closest))
// 	for _, id := range closest {
// 		nh, err := d.networkBehaviour.getNodeHandler(ctx, id)
// 		if err != nil {
// 			return nil, err
// 		}
// 		nodes = append(nodes, nh)
// 	}
// 	return nodes, nil
// }

// // GetValue requests that the node return any value associated with the supplied key.
// // If the node does not have a value for the key it returns core.ErrValueNotFound.
// func (d *Dht[K, A]) GetValue(ctx context.Context, key K) (core.Value[K], error) {
// 	panic("not implemented")
// }

// // PutValue requests that the node stores a value to be associated with the supplied key.
// // If the node cannot or chooses not to store the value for the key it returns core.ErrValueNotAccepted.
// func (d *Dht[K, A]) PutValue(ctx context.Context, r core.Value[K], q int) error {
// 	panic("not implemented")
// }

// // Query traverses the DHT calling fn for each node visited.
// func (d *Dht[K, A]) Query(ctx context.Context, target K, fn core.QueryFunc[K, A]) (core.QueryStats, error) {
// 	ctx, span := util.StartSpan(ctx, "Dht.Query")
// 	defer span.End()

// 	ctx, cancel := context.WithCancel(ctx)
// 	defer cancel()

// 	seeds, err := d.GetClosestNodes(ctx, target, 20)
// 	if err != nil {
// 		return core.QueryStats{}, err
// 	}

// 	seedIDs := make([]kad.NodeID[K], 0, len(seeds))
// 	for _, s := range seeds {
// 		seedIDs = append(seedIDs, s.ID())
// 	}

// 	waiter := NewWaiter[DhtEvent]()
// 	queryID := query.QueryID("foo")

// 	cmd := &EventStartQuery[K, A]{
// 		QueryID:           queryID,
// 		Target:            target,
// 		ProtocolID:        address.ProtocolID("TODO"),
// 		Message:           &fakeMessage[K, A]{key: target},
// 		KnownClosestNodes: seedIDs,
// 		Waiter:            waiter,
// 	}

// 	// queue the start of the query
// 	d.queryBehaviour.Notify(ctx, cmd)

// 	var lastStats core.QueryStats
// 	for {
// 		select {
// 		case <-ctx.Done():
// 			return lastStats, ctx.Err()
// 		case wev := <-waiter.Chan():
// 			ctx, ev := wev.Ctx, wev.Event
// 			switch ev := ev.(type) {
// 			case *EventQueryProgressed[K, A]:
// 				lastStats = core.QueryStats{
// 					Start:    ev.Stats.Start,
// 					Requests: ev.Stats.Requests,
// 					Success:  ev.Stats.Success,
// 					Failure:  ev.Stats.Failure,
// 				}
// 				nh, err := d.networkBehaviour.getNodeHandler(ctx, ev.NodeID)
// 				if err != nil {
// 					// ignore unknown node
// 					break
// 				}

// 				err = fn(ctx, nh, lastStats)
// 				if errors.Is(err, core.SkipRemaining) {
// 					// done
// 					d.queryBehaviour.Notify(ctx, &EventStopQuery{QueryID: queryID})
// 					return lastStats, nil
// 				}
// 				if errors.Is(err, core.SkipNode) {
// 					// TODO: don't add closer nodes from this node
// 					break
// 				}
// 				if err != nil {
// 					// user defined error that terminates the query
// 					d.queryBehaviour.Notify(ctx, &EventStopQuery{QueryID: queryID})
// 					return lastStats, err
// 				}

// 			case *EventQueryFinished:
// 				// query is done
// 				lastStats.Exhausted = true
// 				return lastStats, nil

// 			default:
// 				panic(fmt.Sprintf("unexpected event: %T", ev))
// 			}
// 		}
// 	}
// }

// // AddNodes suggests new DHT nodes and their associated addresses to be added to the routing table.
// // If the routing table is updated as a result of this operation an EventRoutingUpdated notification
// // is emitted on the routing notification channel.
// func (d *Dht[K, A]) AddNodes(ctx context.Context, infos []kad.NodeInfo[K, A]) error {
// 	ctx, span := util.StartSpan(ctx, "Dht.AddNodes")
// 	defer span.End()
// 	for _, info := range infos {
// 		if key.Equal(info.ID().Key(), d.self.Key()) {
// 			// skip self
// 			continue
// 		}

// 		d.routingBehaviour.Notify(ctx, &EventDhtAddNodeInfo[K, A]{
// 			NodeInfo: info,
// 		})

// 	}

// 	return nil
// }

// // Bootstrap instructs the dht to begin bootstrapping the routing table.
// func (d *Dht[K, A]) Bootstrap(ctx context.Context, seeds []kad.NodeID[K]) error {
// 	ctx, span := util.StartSpan(ctx, "Dht.Bootstrap")
// 	defer span.End()
// 	d.routingBehaviour.Notify(ctx, &EventDhtStartBootstrap[K, A]{
// 		// Bootstrap state machine uses the message
// 		Message:   &fakeMessage[K, A]{key: d.self.Key()},
// 		SeedNodes: seeds,
// 	})

// 	return nil
// }
