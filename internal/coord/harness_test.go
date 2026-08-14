package coord

import (
	"github.com/benbjohnson/clock"

	"github.com/probe-lab/zikade/internal/nettest"
	"github.com/probe-lab/zikade/internal/tiny"
)

// The coordinator tests run on the tiny key, node and message types.
type (
	testTopology = nettest.Topology[tiny.Key, tiny.Node, tiny.Message]
	testPeer     = nettest.Peer[tiny.Key, tiny.Node, tiny.Message]
)

var _ nettest.Protocol[tiny.Key, tiny.Node, tiny.Message] = (*tiny.Protocol)(nil)

// linearTopology returns n nodes wired into a linear chain, along with the topology holding
// them. See [nettest.LinearTopology] for the routing tables each node is given.
func linearTopology(n int, clk clock.Clock) (*testTopology, []*testPeer, error) {
	return nettest.LinearTopology[tiny.Key, tiny.Node, tiny.Message](n, clk, tiny.NewProtocol())
}
