package zikade

import (
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/stretchr/testify/require"

	"github.com/probe-lab/zikade/internal/kadtest"
	"github.com/probe-lab/zikade/kadt"
)

// TestRouterRequestTimesOutOnSilentPeer checks that a request to a peer which accepts the
// stream and never answers fails once the request deadline passes, releasing the
// goroutine waiting on it.
func TestRouterRequestTimesOutOnSilentPeer(t *testing.T) {
	ctx := kadtest.CtxShort(t)

	cfg := DefaultConfig()
	cfg.Logger = devnull

	timeout := 100 * time.Millisecond

	listenAddr := libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0")

	local := newTestHost(t, listenAddr)
	t.Cleanup(func() { local.Close() })

	// a peer that accepts the stream, reads the request and never replies
	silent := newTestHost(t, listenAddr)
	t.Cleanup(func() { silent.Close() })
	silent.SetStreamHandler(cfg.ProtocolID, func(s network.Stream) {
		<-ctx.Done()
	})

	local.Peerstore().AddAddrs(silent.ID(), silent.Addrs(), time.Hour)

	tele, err := NewTelemetry(cfg.MeterProvider, cfg.TracerProvider)
	require.NoError(t, err)

	rtr := &router{
		host:       local,
		protocolID: cfg.ProtocolID,
		tele:       tele,
	}

	start := time.Now()
	deadline := start.Add(timeout)

	done := make(chan error, 1)
	go func() {
		_, err := rtr.GetClosestNodes(ctx, kadt.PeerID(silent.ID()), kadt.PeerID(local.ID()).Key(), deadline)
		done <- err
	}()

	select {
	case err := <-done:
		require.Error(t, err)
		// a deadline set in the past would fail the request too, without bounding anything
		require.GreaterOrEqual(t, time.Since(start), timeout)
	case <-time.After(20 * timeout):
		t.Fatalf("request to a silent peer did not fail within %s", 20*timeout)
	}
}
