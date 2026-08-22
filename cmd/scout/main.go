// scout starts a zikade DHT against the public IPFS network and reports how its
// routing table fills. It runs a small terminal UI with a panel for the routing
// table, a panel for the estimated network size, a panel of routing activity read
// from the DHT's metrics, and a command line that accepts "findpeer <peerid>" and
// "exit".
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/iand/xorbie/coordt"
	"github.com/ipfs/go-cid"
	"github.com/ipfs/go-libdht/kad/triert"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/rivo/tview"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

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
		mode         = flag.String("mode", "client", "dht mode, client or server")
		interval     = flag.Duration("interval", 5*time.Second, "how often to refresh the routing table panel")
		sizeInterval = flag.Duration("size-interval", 30*time.Second, "how often to refresh the network size panel")
		level        = flag.String("log", "info", "log level, one of debug, info, warn or error")
		logFile      = flag.String("log-file", "", "file to write logs to, empty to discard")
	)
	flag.Parse()

	// The terminal UI owns the screen, so logs are written to a file if one is named and
	// discarded otherwise, rather than to stdout where they would corrupt the display.
	var w io.Writer = io.Discard
	if *logFile != "" {
		f, err := os.OpenFile(*logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return fmt.Errorf("open log file: %w", err)
		}
		defer f.Close()
		w = f
	}

	logger, err := newLogger(*level, w)
	if err != nil {
		return err
	}

	baseCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ctx, cancel := context.WithCancel(baseCtx)
	defer cancel()

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

	// A manual reader lets the UI pull a fresh snapshot of the DHT's instruments on each refresh
	// tick without any exporter running in the background.
	reader := sdkmetric.NewManualReader()

	cfg := zikade.DefaultConfig()
	cfg.Logger = logger
	cfg.RoutingTable = rt
	cfg.MeterProvider = sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

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

	return runTUI(ctx, cancel, d, rt, reader, *interval, *sizeInterval)
}

// runTUI builds and runs the terminal UI. cancel stops the background refresh when the user
// exits, so the refresh does not outlive the screen it draws to.
func runTUI(ctx context.Context, cancel context.CancelFunc, d *zikade.DHT, rt *triert.TrieRT[kadt.Key, kadt.PeerID], reader *sdkmetric.ManualReader, interval, sizeInterval time.Duration) error {
	app := tview.NewApplication()

	rtPanel := tview.NewTextView()
	rtPanel.SetBorder(true)
	rtPanel.SetTitle(" routing table ")

	nsPanel := tview.NewTextView()
	nsPanel.SetBorder(true)
	nsPanel.SetTitle(" network size ")

	metricsPanel := tview.NewTextView()
	metricsPanel.SetBorder(true)
	metricsPanel.SetTitle(" routing activity ")

	tracePanel := tview.NewTextView()
	tracePanel.SetBorder(true)
	tracePanel.SetTitle(" query trace ")
	tracePanel.SetMaxLines(500)

	status := tview.NewTextView()
	status.SetTextColor(tcell.ColorGray)

	input := tview.NewInputField()
	input.SetLabel("> ")

	start := time.Now()

	// trace appends a line to the query trace panel. It is called from the goroutine that runs
	// a query, so the write goes through QueueUpdateDraw.
	trace := func(line string) {
		app.QueueUpdateDraw(func() {
			fmt.Fprintln(tracePanel, line)
			tracePanel.ScrollToEnd()
		})
	}

	// The panels carry their first values before the UI starts, so it opens with content
	// rather than waiting for the first tick.
	rtPanel.SetText(rtText(rt, start))
	nsPanel.SetText(nsText(d, start))
	metricsPanel.SetText(metricsText(ctx, reader, rt))
	const helpText = "commands: findpeer <peerid>, findproviders <cid>, exit"
	status.SetText(helpText)

	input.SetDoneFunc(func(key tcell.Key) {
		if key != tcell.KeyEnter {
			return
		}

		text := strings.TrimSpace(input.GetText())
		input.SetText("")
		if text == "" {
			return
		}

		fields := strings.Fields(text)
		switch fields[0] {
		case "exit":
			cancel()
			app.Stop()
		case "findpeer":
			if len(fields) < 2 {
				status.SetText("usage: findpeer <peerid>")
				return
			}
			id := fields[1]
			status.SetText("finding " + id + " ...")
			go func() {
				trace("findpeer " + id)
				runFindPeer(context.Background(), d, id, trace)
				app.QueueUpdateDraw(func() { status.SetText(helpText) })
			}()
		case "findproviders":
			if len(fields) < 2 {
				status.SetText("usage: findproviders <cid>")
				return
			}
			key := fields[1]
			status.SetText("finding providers for " + key + " ...")
			go func() {
				trace("findproviders " + key)
				runFindProviders(context.Background(), d, key, trace)
				app.QueueUpdateDraw(func() { status.SetText(helpText) })
			}()
		default:
			status.SetText("unknown command: " + fields[0])
		}
	})

	// tcell puts the terminal in raw mode, so Ctrl-C arrives as a key event rather than a
	// signal. Handle it here so the UI can still be closed that way.
	app.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyCtrlC {
			cancel()
			app.Stop()
			return nil
		}
		return ev
	})

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(rtPanel, 5, 0, false).
		AddItem(nsPanel, 7, 0, false).
		AddItem(metricsPanel, 13, 0, false).
		AddItem(tracePanel, 0, 1, false).
		AddItem(status, 1, 0, false).
		AddItem(input, 1, 0, true)

	// A signal cancels ctx, which stops the UI.
	go func() {
		<-ctx.Done()
		app.Stop()
	}()

	go refresh(ctx, app, d, rt, reader, rtPanel, nsPanel, metricsPanel, start, interval, sizeInterval)

	if err := app.SetRoot(layout, true).Run(); err != nil {
		return fmt.Errorf("run tui: %w", err)
	}

	return nil
}

// refresh repaints the two panels on their own tickers until ctx is cancelled. The estimate
// moves only as lookups complete, so it is refreshed on its own slower cadence.
func refresh(ctx context.Context, app *tview.Application, d *zikade.DHT, rt *triert.TrieRT[kadt.Key, kadt.PeerID], reader *sdkmetric.ManualReader, rtPanel, nsPanel, metricsPanel *tview.TextView, start time.Time, interval, sizeInterval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	sizeTicker := time.NewTicker(sizeInterval)
	defer sizeTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rtLine := rtText(rt, start)
			metricsLine := metricsText(ctx, reader, rt)
			app.QueueUpdateDraw(func() {
				rtPanel.SetText(rtLine)
				metricsPanel.SetText(metricsLine)
			})
		case <-sizeTicker.C:
			app.QueueUpdateDraw(func() { nsPanel.SetText(nsText(d, start)) })
		}
	}
}

// metricsText renders the routing activity panel from a snapshot of the DHT's instruments. The
// counters are cumulative totals since the node started; the survey and region lines stay at zero
// until the survey is enabled.
func metricsText(ctx context.Context, reader *sdkmetric.ManualReader, rt *triert.TrieRT[kadt.Key, kadt.PeerID]) string {
	m, err := collectMetrics(ctx, reader)
	if err != nil {
		return "metrics unavailable: " + err.Error()
	}

	get := func(name string) int64 { return int64(m[name]) }

	return fmt.Sprintf(
		"network:   size=%d  in-flight=%d  handlers=%d\n"+
			"bootstrap: running=%d  sent=%d ok=%d fail=%d\n"+
			"explore:   running=%d  sent=%d ok=%d fail=%d\n"+
			"survey:    running=%d  sent=%d ok=%d fail=%d\n"+
			"include:   queued=%d  sent=%d ok=%d fail=%d\n"+
			"probe:     pending=%d  sent=%d ok=%d fail=%d\n"+
			"regions:   count=%d  oldest=%ds  done=%d timeout=%d fail=%d\n"+
			"table:     size=%d  buckets=%d  added=%d removed=%d\n"+
			"loop:      passes=%d  queue=%d dropped=%d\n"+
			"%s",
		get("network_size"), get("network_requests_in_flight"), get("network_node_handlers"),
		get("bootstrap_running"), get("bootstrap_find_sent"), get("bootstrap_find_succeeded"), get("bootstrap_find_failed"),
		get("explore_running"), get("explore_find_sent"), get("explore_find_succeeded"), get("explore_find_failed"),
		get("survey_running"), get("survey_find_sent"), get("survey_find_succeeded"), get("survey_find_failed"),
		get("include_candidate_count"), get("include_checks_sent"), get("include_checks_passed"), get("include_checks_failed"),
		get("probe_pending_count"), get("probe_checks_sent"), get("probe_checks_passed"), get("probe_checks_failed"),
		get("survey_regions"), get("survey_oldest_region_age_seconds"), get("surveys_completed"), get("surveys_timeout"), get("surveys_failed"),
		get("routing_table_size"), get("routing_table_buckets_occupied"), get("routing_table_additions"), get("routing_table_removals"),
		get("coordinator_event_loop_passes"), get("routing_inbound_queue_depth"), get("routing_inbound_events_dropped"),
		bucketsLine(rt),
	)
}

// bucketsLine renders the size of each routing table bucket by common prefix length, reading the
// table directly. It shows one number per cpl from zero up to the highest occupied bucket.
func bucketsLine(rt *triert.TrieRT[kadt.Key, kadt.PeerID]) string {
	maxCpl := -1
	for cpl := range 256 {
		if rt.CplSize(cpl) > 0 {
			maxCpl = cpl
		}
	}
	if maxCpl < 0 {
		return "buckets:   (empty)"
	}

	var b strings.Builder
	b.WriteString("buckets:   ")
	for cpl := 0; cpl <= maxCpl; cpl++ {
		fmt.Fprintf(&b, "%d ", rt.CplSize(cpl))
	}
	return strings.TrimRight(b.String(), " ")
}

// collectMetrics pulls a snapshot from the reader and flattens the int64 and float64 counters and
// gauges into a map keyed by instrument name, summing the data points of each instrument. Histograms
// are not included. A tagged instrument's series are summed together, so this loses per-attribute
// detail.
func collectMetrics(ctx context.Context, reader *sdkmetric.ManualReader) (map[string]float64, error) {
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		return nil, err
	}

	vals := make(map[string]float64)
	for _, sm := range rm.ScopeMetrics {
		for _, inst := range sm.Metrics {
			switch data := inst.Data.(type) {
			case metricdata.Sum[int64]:
				for _, dp := range data.DataPoints {
					vals[inst.Name] += float64(dp.Value)
				}
			case metricdata.Gauge[int64]:
				for _, dp := range data.DataPoints {
					vals[inst.Name] += float64(dp.Value)
				}
			case metricdata.Sum[float64]:
				for _, dp := range data.DataPoints {
					vals[inst.Name] += dp.Value
				}
			case metricdata.Gauge[float64]:
				for _, dp := range data.DataPoints {
					vals[inst.Name] += dp.Value
				}
			}
		}
	}

	return vals, nil
}

// rtText renders the routing table panel: how long the node has run, how many nodes the table
// holds, and how many common prefix lengths are occupied.
func rtText(rt *triert.TrieRT[kadt.Key, kadt.PeerID], start time.Time) string {
	return fmt.Sprintf("elapsed:       %s\nsize:          %d\noccupied CPLs: %d",
		time.Since(start).Round(time.Second), rt.Size(), occupiedCpls(rt))
}

// nsText renders the network size panel, or why there is no estimate yet. An estimate needs
// several completed lookups, so a node that has just started has none.
func nsText(d *zikade.DHT, start time.Time) string {
	elapsed := time.Since(start).Round(time.Second)

	est, err := d.NetworkSize()
	if err != nil {
		return fmt.Sprintf("elapsed: %s\nsize:    unknown (%s)", elapsed, err.Error())
	}

	return fmt.Sprintf("elapsed: %s\nsize:    %d\nstd err: %.1f\nfit:     %.2f\nsamples: %d",
		elapsed, est.Size, est.StdErr, est.Fit, est.Samples)
}

// runFindPeer runs a FindPeer for id, tracing each node contacted and then the outcome with any
// addresses found. It is the cheapest end to end exercise of a query.
func runFindPeer(ctx context.Context, d *zikade.DHT, id string, trace func(string)) {
	pid, err := peer.Decode(id)
	if err != nil {
		trace(fmt.Sprintf("  bad peer id: %s", err))
		return
	}

	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	start := time.Now()
	addrInfo, err := d.FindPeerProgress(ctx, pid, func(node peer.ID, stats coordt.QueryStats) {
		trace(fmt.Sprintf("  contacted %s  req=%d ok=%d fail=%d", shorten(node), stats.Requests, stats.Success, stats.Failure))
	})
	if err != nil {
		trace(fmt.Sprintf("  not found after %s: %s", time.Since(start).Round(time.Millisecond), err))
		return
	}

	trace(fmt.Sprintf("  found %s with %d addrs in %s", shorten(addrInfo.ID), len(addrInfo.Addrs), time.Since(start).Round(time.Millisecond)))
	for _, a := range addrInfo.Addrs {
		trace("    " + a.String())
	}
}

// runFindProviders looks up every provider for key, tracing each node contacted and then the
// providers found with their addresses.
func runFindProviders(ctx context.Context, d *zikade.DHT, key string, trace func(string)) {
	c, err := cid.Decode(key)
	if err != nil {
		trace(fmt.Sprintf("  bad cid: %s", err))
		return
	}

	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	start := time.Now()
	providers, err := d.FindProvidersProgress(ctx, c, 0, func(node peer.ID, stats coordt.QueryStats) {
		trace(fmt.Sprintf("  contacted %s  req=%d ok=%d fail=%d", shorten(node), stats.Requests, stats.Success, stats.Failure))
	})
	if err != nil {
		trace(fmt.Sprintf("  failed after %s: %s", time.Since(start).Round(time.Millisecond), err))
		return
	}

	trace(fmt.Sprintf("  found %d providers in %s", len(providers), time.Since(start).Round(time.Millisecond)))
	for _, p := range providers {
		trace(fmt.Sprintf("    %s  (%d addrs)", shorten(p.ID), len(p.Addrs)))
		for _, a := range p.Addrs {
			trace("      " + a.String())
		}
	}
}

// shorten abbreviates a peer id so a trace line stays on one row.
func shorten(id peer.ID) string {
	s := id.String()
	if len(s) <= 16 {
		return s
	}
	return s[:8] + ".." + s[len(s)-6:]
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

func newLogger(level string, w io.Writer) (*slog.Logger, error) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("log level: %w", err)
	}

	logger := slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: l}))
	slog.SetDefault(logger)

	return logger, nil
}
