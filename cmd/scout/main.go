// scout starts a zikade DHT against the public IPFS network and reports how its
// routing table fills.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"time"

	"github.com/iand/pontium/hlog"
	"github.com/ipfs/go-libdht/kad/triert"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/probe-lab/zikade"
	"github.com/probe-lab/zikade/kadt"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		mode     = flag.String("mode", "client", "dht mode, client or server")
		duration = flag.Duration("duration", time.Minute, "how long to run for, zero to run until interrupted")
		interval = flag.Duration("interval", 5*time.Second, "how often to report the routing table")
		findPeer = flag.String("find-peer", "", "peer id to look up once the routing table has nodes in it")
		level    = flag.String("log", "info", "log level, one of debug, info, warn or error")
	)
	flag.Parse()

	logger, err := newLogger(*level)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if *duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}

	h, err := libp2p.New()
	if err != nil {
		return fmt.Errorf("new host: %w", err)
	}
	defer h.Close()

	logger.Info("host listening", "peer_id", h.ID().String(), "addrs", len(h.Addrs()))

	rt, err := triert.New[kadt.Key, kadt.PeerID](kadt.PeerID(h.ID()), nil)
	if err != nil {
		return fmt.Errorf("new routing table: %w", err)
	}

	cfg := zikade.DefaultConfig()
	cfg.Logger = logger
	cfg.RoutingTable = rt

	switch *mode {
	case "client":
		cfg.Mode = zikade.ModeOptClient
	case "server":
		cfg.Mode = zikade.ModeOptServer
	default:
		return fmt.Errorf("unknown mode %q", *mode)
	}

	d, err := zikade.New(h, cfg)
	if err != nil {
		return fmt.Errorf("new dht: %w", err)
	}
	defer d.Close()

	logger.Info("dht started, nothing has asked it to bootstrap", "mode", *mode, "bootstrap_peers", len(cfg.BootstrapPeers))

	report(ctx, logger, rt, *interval)

	if *findPeer != "" {
		if err := lookup(context.Background(), logger, d, *findPeer); err != nil {
			return err
		}
	}

	logger.Info("done", "routing_table_size", rt.Size(), "occupied_cpls", occupiedCpls(rt))

	return nil
}

// report prints the routing table's size until ctx is done, noting the first time a node
// appears. The DHT starts a bootstrap of its own accord when its table is short of nodes.
func report(ctx context.Context, logger *slog.Logger, rt *triert.TrieRT[kadt.Key, kadt.PeerID], interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	start := time.Now()
	seen := false

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			size := rt.Size()
			if size > 0 && !seen {
				seen = true
				logger.Info("routing table has its first node", "elapsed", time.Since(start).Round(time.Millisecond))
			}
			logger.Info("routing table", "elapsed", time.Since(start).Round(time.Second), "size", size, "occupied_cpls", occupiedCpls(rt))
		}
	}
}

// lookup runs a FindPeer for id, which is the cheapest end to end exercise of a query.
func lookup(ctx context.Context, logger *slog.Logger, d *zikade.DHT, id string) error {
	pid, err := peer.Decode(id)
	if err != nil {
		return fmt.Errorf("decode peer id: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	start := time.Now()
	addrInfo, err := d.FindPeer(ctx, pid)
	if err != nil {
		logger.Warn("find peer failed", "peer_id", id, "elapsed", time.Since(start).Round(time.Millisecond), "error", err)
		return nil
	}

	logger.Info("found peer", "peer_id", addrInfo.ID.String(), "addrs", len(addrInfo.Addrs), "elapsed", time.Since(start).Round(time.Millisecond))

	return nil
}

// occupiedCpls counts the common prefix lengths holding at least one node.
func occupiedCpls(rt *triert.TrieRT[kadt.Key, kadt.PeerID]) int {
	n := 0
	for cpl := range 256 {
		if rt.CplSize(cpl) > 0 {
			n++
		}
	}
	return n
}

func newLogger(level string) (*slog.Logger, error) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("log level: %w", err)
	}

	h := new(hlog.Handler)
	h = h.WithSource()
	h = h.WithLevel(l)

	logger := slog.New(h)
	slog.SetDefault(logger)

	return logger, nil
}
