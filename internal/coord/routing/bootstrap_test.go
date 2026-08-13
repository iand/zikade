package routing

import (
	"context"
	"testing"
	"time"

	"github.com/ipfs/go-libdht/kad/key"
	"github.com/stretchr/testify/require"

	"github.com/probe-lab/zikade/internal/coord/coordt"
	"github.com/probe-lab/zikade/internal/tiny"
)

func TestBootstrapConfigValidate(t *testing.T) {
	t.Run("default is valid", func(t *testing.T) {
		cfg := DefaultBootstrapConfig()
		require.NoError(t, cfg.Validate())
	})

	t.Run("tracer is not nil", func(t *testing.T) {
		cfg := DefaultBootstrapConfig()
		cfg.Tracer = nil
		require.Error(t, cfg.Validate())
	})

	t.Run("meter is not nil", func(t *testing.T) {
		cfg := DefaultBootstrapConfig()
		cfg.Meter = nil
		require.Error(t, cfg.Validate())
	})

	t.Run("timeout positive", func(t *testing.T) {
		cfg := DefaultBootstrapConfig()
		cfg.Timeout = 0
		require.Error(t, cfg.Validate())
		cfg.Timeout = -1
		require.Error(t, cfg.Validate())
	})

	t.Run("request concurrency positive", func(t *testing.T) {
		cfg := DefaultBootstrapConfig()
		cfg.RequestConcurrency = 0
		require.Error(t, cfg.Validate())
		cfg.RequestConcurrency = -1
		require.Error(t, cfg.Validate())
	})

	t.Run("request timeout positive", func(t *testing.T) {
		cfg := DefaultBootstrapConfig()
		cfg.RequestTimeout = 0
		require.Error(t, cfg.Validate())
		cfg.RequestTimeout = -1
		require.Error(t, cfg.Validate())
	})
}

func TestBootstrapStartsIdle(t *testing.T) {
	ctx := context.Background()
	now := epoch
	cfg := DefaultBootstrapConfig()

	self := tiny.NewNode(0)
	bs, err := NewBootstrap[tiny.Key](self, cfg)
	require.NoError(t, err)

	state := bs.Advance(ctx, now, &EventBootstrapPoll{})
	require.IsType(t, &StateBootstrapIdle{}, state)
}

func TestBootstrapStart(t *testing.T) {
	ctx := context.Background()
	now := epoch
	cfg := DefaultBootstrapConfig()

	self := tiny.NewNode(0)
	bs, err := NewBootstrap[tiny.Key](self, cfg)
	require.NoError(t, err)

	a := tiny.NewNode(0b00000100) // 4

	// start the bootstrap
	state := bs.Advance(ctx, now, &EventBootstrapStart[tiny.Key, tiny.Node]{
		KnownClosestNodes: []tiny.Node{a},
	})
	require.IsType(t, &StateBootstrapFindCloser[tiny.Key, tiny.Node]{}, state)

	// the query should attempt to contact the node it was given
	st := state.(*StateBootstrapFindCloser[tiny.Key, tiny.Node])

	// the query should be the one just added
	require.Equal(t, coordt.QueryID("bootstrap"), st.QueryID)

	// the query should attempt to contact the node it was given
	require.Equal(t, a, st.NodeID)

	// with the correct key
	require.True(t, key.Equal(self.Key(), st.Target))

	// now the bootstrap reports that it is waiting
	state = bs.Advance(ctx, now, &EventBootstrapPoll{})
	require.IsType(t, &StateBootstrapWaiting{}, state)
}

func TestBootstrapMessageResponse(t *testing.T) {
	ctx := context.Background()
	now := epoch
	cfg := DefaultBootstrapConfig()

	self := tiny.NewNode(0)
	bs, err := NewBootstrap[tiny.Key](self, cfg)
	require.NoError(t, err)

	a := tiny.NewNode(0b00000100) // 4

	// start the bootstrap
	state := bs.Advance(ctx, now, &EventBootstrapStart[tiny.Key, tiny.Node]{
		KnownClosestNodes: []tiny.Node{a},
	})
	require.IsType(t, &StateBootstrapFindCloser[tiny.Key, tiny.Node]{}, state)

	// the bootstrap should attempt to contact the node it was given
	st := state.(*StateBootstrapFindCloser[tiny.Key, tiny.Node])
	require.Equal(t, coordt.QueryID("bootstrap"), st.QueryID)
	require.Equal(t, a, st.NodeID)

	// notify bootstrap that node was contacted successfully, but no closer nodes
	state = bs.Advance(ctx, now, &EventBootstrapFindCloserResponse[tiny.Key, tiny.Node]{
		NodeID: a,
	})

	// bootstrap should respond that its query has finished
	require.IsType(t, &StateBootstrapFinished{}, state)

	stf := state.(*StateBootstrapFinished)
	require.Equal(t, 1, stf.Stats.Requests)
	require.Equal(t, 1, stf.Stats.Success)
}

func TestBootstrapProgress(t *testing.T) {
	ctx := context.Background()
	now := epoch
	cfg := DefaultBootstrapConfig()
	cfg.RequestConcurrency = 3 // 1 less than the 4 nodes to be visited

	self := tiny.NewNode(0)
	bs, err := NewBootstrap[tiny.Key](self, cfg)
	require.NoError(t, err)

	a := tiny.NewNode(0b00000100) // 4
	b := tiny.NewNode(0b00001000) // 8
	c := tiny.NewNode(0b00010000) // 16
	d := tiny.NewNode(0b00100000) // 32

	// ensure the order of the known nodes
	require.True(t, self.Key().Xor(a.Key()).Compare(self.Key().Xor(b.Key())) == -1)
	require.True(t, self.Key().Xor(b.Key()).Compare(self.Key().Xor(c.Key())) == -1)
	require.True(t, self.Key().Xor(c.Key()).Compare(self.Key().Xor(d.Key())) == -1)

	// start the bootstrap
	state := bs.Advance(ctx, now, &EventBootstrapStart[tiny.Key, tiny.Node]{
		KnownClosestNodes: []tiny.Node{d, a, b, c},
	})

	// the bootstrap should attempt to contact the closest node it was given
	require.IsType(t, &StateBootstrapFindCloser[tiny.Key, tiny.Node]{}, state)
	st := state.(*StateBootstrapFindCloser[tiny.Key, tiny.Node])
	require.Equal(t, coordt.QueryID("bootstrap"), st.QueryID)
	require.Equal(t, a, st.NodeID)

	// next the bootstrap attempts to contact second nearest node
	state = bs.Advance(ctx, now, &EventBootstrapPoll{})
	require.IsType(t, &StateBootstrapFindCloser[tiny.Key, tiny.Node]{}, state)
	st = state.(*StateBootstrapFindCloser[tiny.Key, tiny.Node])
	require.Equal(t, b, st.NodeID)

	// next the bootstrap attempts to contact third nearest node
	state = bs.Advance(ctx, now, &EventBootstrapPoll{})
	require.IsType(t, &StateBootstrapFindCloser[tiny.Key, tiny.Node]{}, state)
	st = state.(*StateBootstrapFindCloser[tiny.Key, tiny.Node])
	require.Equal(t, c, st.NodeID)

	// now the bootstrap should be waiting since it is at request capacity
	state = bs.Advance(ctx, now, &EventBootstrapPoll{})
	require.IsType(t, &StateBootstrapWaiting{}, state)

	// notify bootstrap that node was contacted successfully, but no closer nodes
	state = bs.Advance(ctx, now, &EventBootstrapFindCloserResponse[tiny.Key, tiny.Node]{
		NodeID: a,
	})

	// now the bootstrap has capacity to contact fourth nearest node
	require.IsType(t, &StateBootstrapFindCloser[tiny.Key, tiny.Node]{}, state)
	st = state.(*StateBootstrapFindCloser[tiny.Key, tiny.Node])
	require.Equal(t, d, st.NodeID)

	// notify bootstrap that a node was contacted successfully
	state = bs.Advance(ctx, now, &EventBootstrapFindCloserResponse[tiny.Key, tiny.Node]{
		NodeID: b,
	})

	// bootstrap should respond that it is waiting for messages
	require.IsType(t, &StateBootstrapWaiting{}, state)

	// notify bootstrap that a node was contacted successfully
	state = bs.Advance(ctx, now, &EventBootstrapFindCloserResponse[tiny.Key, tiny.Node]{
		NodeID: c,
	})

	// bootstrap should respond that it is waiting for last message
	require.IsType(t, &StateBootstrapWaiting{}, state)

	// notify bootstrap that the final node was contacted successfully
	state = bs.Advance(ctx, now, &EventBootstrapFindCloserResponse[tiny.Key, tiny.Node]{
		NodeID: d,
	})

	// bootstrap should respond that its query has finished
	require.IsType(t, &StateBootstrapFinished{}, state)

	stf := state.(*StateBootstrapFinished)
	require.Equal(t, 4, stf.Stats.Requests)
	require.Equal(t, 4, stf.Stats.Success)
}

func TestBootstrapFinishesThenGoesIdle(t *testing.T) {
	ctx := context.Background()
	now := epoch
	cfg := DefaultBootstrapConfig()

	self := tiny.NewNode(0)
	bs, err := NewBootstrap[tiny.Key](self, cfg)
	require.NoError(t, err)

	a := tiny.NewNode(0b00000100) // 4

	// start the bootstrap
	state := bs.Advance(ctx, now, &EventBootstrapStart[tiny.Key, tiny.Node]{
		KnownClosestNodes: []tiny.Node{a},
	})
	require.IsType(t, &StateBootstrapFindCloser[tiny.Key, tiny.Node]{}, state)

	// the bootstrap should attempt to contact the node it was given
	st := state.(*StateBootstrapFindCloser[tiny.Key, tiny.Node])
	require.Equal(t, coordt.QueryID("bootstrap"), st.QueryID)
	require.Equal(t, a, st.NodeID)

	// notify bootstrap that node was contacted successfully, but no closer nodes
	state = bs.Advance(ctx, now, &EventBootstrapFindCloserResponse[tiny.Key, tiny.Node]{
		NodeID: a,
	})

	// bootstrap should respond that its query has finished
	require.IsType(t, &StateBootstrapFinished{}, state)

	// poll bootstrap
	state = bs.Advance(ctx, now, &EventBootstrapPoll{})

	// bootstrap should now be idle
	require.IsType(t, &StateBootstrapIdle{}, state)
}

func TestBootstrapFinishedIgnoresLaterResponses(t *testing.T) {
	ctx := context.Background()
	now := epoch
	cfg := DefaultBootstrapConfig()

	self := tiny.NewNode(0)
	bs, err := NewBootstrap[tiny.Key](self, cfg)
	require.NoError(t, err)

	a := tiny.NewNode(4)
	b := tiny.NewNode(8)

	// start the bootstrap
	state := bs.Advance(ctx, now, &EventBootstrapStart[tiny.Key, tiny.Node]{
		KnownClosestNodes: []tiny.Node{b},
	})
	require.IsType(t, &StateBootstrapFindCloser[tiny.Key, tiny.Node]{}, state)

	// the bootstrap should attempt to contact the node it was given
	st := state.(*StateBootstrapFindCloser[tiny.Key, tiny.Node])
	require.Equal(t, coordt.QueryID("bootstrap"), st.QueryID)
	require.Equal(t, b, st.NodeID)

	// notify bootstrap that node was contacted successfully with a closer node
	state = bs.Advance(ctx, now, &EventBootstrapFindCloserResponse[tiny.Key, tiny.Node]{
		NodeID:      b,
		CloserNodes: []tiny.Node{a},
	})

	// bootstrap should respond that it wants to contact the new node
	require.IsType(t, &StateBootstrapFindCloser[tiny.Key, tiny.Node]{}, state)

	// poll bootstrap
	state = bs.Advance(ctx, now, &EventBootstrapPoll{})

	// bootstrap should now be waiting
	require.IsType(t, &StateBootstrapWaiting{}, state)

	// advance the clock past the timeout
	now = now.Add(cfg.RequestTimeout * 2)

	// poll bootstrap
	state = bs.Advance(ctx, now, &EventBootstrapPoll{})

	// bootstrap should now be finished
	require.IsType(t, &StateBootstrapFinished{}, state)

	// notify bootstrap that node was contacted successfully after the timeout
	state = bs.Advance(ctx, now, &EventBootstrapFindCloserResponse[tiny.Key, tiny.Node]{
		NodeID: a,
	})

	// bootstrap should ignore late message and now be idle
	require.IsType(t, &StateBootstrapIdle{}, state)
}

func TestBootstrapFinishedIgnoresLaterFailures(t *testing.T) {
	ctx := context.Background()
	now := epoch
	cfg := DefaultBootstrapConfig()

	self := tiny.NewNode(0)
	bs, err := NewBootstrap[tiny.Key](self, cfg)
	require.NoError(t, err)

	a := tiny.NewNode(4)
	b := tiny.NewNode(8)

	// start the bootstrap
	state := bs.Advance(ctx, now, &EventBootstrapStart[tiny.Key, tiny.Node]{
		KnownClosestNodes: []tiny.Node{b},
	})
	require.IsType(t, &StateBootstrapFindCloser[tiny.Key, tiny.Node]{}, state)

	// the bootstrap should attempt to contact the node it was given
	st := state.(*StateBootstrapFindCloser[tiny.Key, tiny.Node])
	require.Equal(t, coordt.QueryID("bootstrap"), st.QueryID)
	require.Equal(t, b, st.NodeID)

	// notify bootstrap that node was contacted successfully with a closer node
	state = bs.Advance(ctx, now, &EventBootstrapFindCloserResponse[tiny.Key, tiny.Node]{
		NodeID:      b,
		CloserNodes: []tiny.Node{a},
	})

	// bootstrap should respond that it wants to contact the new node
	require.IsType(t, &StateBootstrapFindCloser[tiny.Key, tiny.Node]{}, state)

	// poll bootstrap
	state = bs.Advance(ctx, now, &EventBootstrapPoll{})

	// bootstrap should now be waiting
	require.IsType(t, &StateBootstrapWaiting{}, state)

	// advance the clock past the timeout
	now = now.Add(cfg.RequestTimeout * 2)

	// poll bootstrap
	state = bs.Advance(ctx, now, &EventBootstrapPoll{})

	// bootstrap should now be finished
	require.IsType(t, &StateBootstrapFinished{}, state)

	// notify bootstrap that node failed to be contacted
	state = bs.Advance(ctx, now, &EventBootstrapFindCloserFailure[tiny.Key, tiny.Node]{
		NodeID: a,
	})

	// bootstrap should ignore late message and now be idle
	require.IsType(t, &StateBootstrapIdle{}, state)
}

func TestBootstrapTimeout(t *testing.T) {
	ctx := context.Background()
	now := epoch
	cfg := DefaultBootstrapConfig()
	cfg.Timeout = 3 * time.Minute

	// the request must outlive the bootstrap so its query is still waiting for a
	// response when the bootstrap deadline passes
	cfg.RequestTimeout = time.Hour

	self := tiny.NewNode(0)
	bs, err := NewBootstrap[tiny.Key](self, cfg)
	require.NoError(t, err)

	a := tiny.NewNode(0b00000100) // 4

	state := bs.Advance(ctx, now, &EventBootstrapStart[tiny.Key, tiny.Node]{
		KnownClosestNodes: []tiny.Node{a},
	})
	require.IsType(t, &StateBootstrapFindCloser[tiny.Key, tiny.Node]{}, state)

	// the node never responds, but the bootstrap has not run out of time yet
	now = now.Add(cfg.Timeout - time.Second)
	state = bs.Advance(ctx, now, &EventBootstrapPoll{})
	require.IsType(t, &StateBootstrapWaiting{}, state)

	// once the deadline passes the bootstrap gives up
	now = now.Add(2 * time.Second)
	state = bs.Advance(ctx, now, &EventBootstrapPoll{})
	require.IsType(t, &StateBootstrapTimeout{}, state)
}

func TestBootstrapReportsNextDue(t *testing.T) {
	ctx := context.Background()
	now := epoch
	cfg := DefaultBootstrapConfig()
	cfg.Timeout = 3 * time.Minute
	cfg.RequestTimeout = time.Minute

	self := tiny.NewNode(0)
	bs, err := NewBootstrap[tiny.Key](self, cfg)
	require.NoError(t, err)

	// nothing running, nothing due
	state := bs.Advance(ctx, now, &EventBootstrapPoll{})
	require.IsType(t, &StateBootstrapIdle{}, state)

	state = bs.Advance(ctx, now, &EventBootstrapStart[tiny.Key, tiny.Node]{
		KnownClosestNodes: []tiny.Node{tiny.NewNode(0b00000100)},
	})
	require.IsType(t, &StateBootstrapFindCloser[tiny.Key, tiny.Node]{}, state)

	// the request deadline falls before the bootstrap deadline, so it is reported
	state = bs.Advance(ctx, now, &EventBootstrapPoll{})
	require.IsType(t, &StateBootstrapWaiting{}, state)
	require.Equal(t, now.Add(cfg.RequestTimeout), state.(*StateBootstrapWaiting).NextDue)
}

func TestBootstrapReportsOwnDeadlineWhenSooner(t *testing.T) {
	ctx := context.Background()
	now := epoch
	cfg := DefaultBootstrapConfig()
	cfg.Timeout = time.Minute
	cfg.RequestTimeout = time.Hour

	self := tiny.NewNode(0)
	bs, err := NewBootstrap[tiny.Key](self, cfg)
	require.NoError(t, err)

	state := bs.Advance(ctx, now, &EventBootstrapStart[tiny.Key, tiny.Node]{
		KnownClosestNodes: []tiny.Node{tiny.NewNode(0b00000100)},
	})
	require.IsType(t, &StateBootstrapFindCloser[tiny.Key, tiny.Node]{}, state)

	// the bootstrap gives up before the request does, so its own deadline wins
	state = bs.Advance(ctx, now, &EventBootstrapPoll{})
	require.IsType(t, &StateBootstrapWaiting{}, state)
	require.Equal(t, now.Add(cfg.Timeout), state.(*StateBootstrapWaiting).NextDue)
}
