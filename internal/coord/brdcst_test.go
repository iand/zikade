package coord

import (
	"testing"

	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/require"

	"github.com/probe-lab/zikade/internal/coord/brdcst"
	"github.com/probe-lab/zikade/internal/kadtest"
	"github.com/probe-lab/zikade/internal/nettest"
	"github.com/probe-lab/zikade/kadt"
	"github.com/probe-lab/zikade/pb"
	"github.com/probe-lab/zikade/tele"
)

// TestBroadcastBehaviourContactsAllSeeds asserts that a static broadcast sends
// its record to every seed node without waiting for a response from each one in
// turn.
//
// The behaviour is driven exactly as [Coordinator.eventLoop] drives it, which
// is the point: the static strategy holds no concurrency limit and is willing
// to contact every seed, but it only gets the chance if the behaviour keeps
// signalling that it is ready.
func TestBroadcastBehaviourContactsAllSeeds(t *testing.T) {
	ctx := kadtest.CtxShort(t)

	_, nodes, err := nettest.LinearTopology(6, clock.New())
	require.NoError(t, err)

	self := nodes[0].NodeID

	pool, err := brdcst.NewPool[kadt.Key, kadt.PeerID, *pb.Message](self, nil)
	require.NoError(t, err)

	b := NewPooledBroadcastBehaviour(pool, tele.DefaultLogger("coord"), tele.NoopTracer())

	seeds := []kadt.PeerID{
		nodes[1].NodeID,
		nodes[2].NodeID,
		nodes[3].NodeID,
		nodes[4].NodeID,
		nodes[5].NodeID,
	}

	msg := &pb.Message{Type: pb.Message_PUT_VALUE}

	b.Notify(ctx, &EventStartBroadcast{
		QueryID: "test",
		Target:  msg.Target(),
		Message: msg,
		Seed:    seeds,
		Config:  brdcst.DefaultConfigStatic(),
		Notify:  NewBroadcastWaiter(0),
	})

	evs := PerformWhileReady(t, ctx, b)

	var contacted []kadt.PeerID
	for _, ev := range evs {
		if oev, ok := ev.(*EventOutboundSendMessage); ok {
			contacted = append(contacted, oev.To)
		}
	}

	require.ElementsMatch(t, seeds, contacted, "expected every seed to be sent the record")
}
