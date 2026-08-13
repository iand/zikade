package zikade

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	kaddht "github.com/libp2p/go-libp2p-kad-dht"
	refpb "github.com/libp2p/go-libp2p-kad-dht/pb"
	record "github.com/libp2p/go-libp2p-record"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/libp2p/go-msgio"
	"github.com/libp2p/go-msgio/pbio"
	"google.golang.org/protobuf/proto"

	"github.com/probe-lab/zikade/kadt"
	"github.com/probe-lab/zikade/pb"
)

// These tests exercise the wire protocol between zikade and the reference
// implementation, github.com/libp2p/go-libp2p-kad-dht. They run two real hosts
// over loopback TCP and speak the Amino protocol between them.

// interopTimeout bounds each interop test.
const interopTimeout = 20 * time.Second

const (
	// interopSettleTimeout bounds how long a test waits for the effect of a message
	// that draws no response to become observable. Such a message and the request
	// that checks it travel on separate streams, so the check can overtake it.
	interopSettleTimeout = 2 * time.Second

	// interopPollInterval is the gap between checks while waiting out
	// interopSettleTimeout.
	interopPollInterval = 10 * time.Millisecond
)

// refMessageSender sends messages to a peer speaking the Amino protocol.
type refMessageSender struct {
	host       host.Host
	protocolID protocol.ID
}

var _ refpb.MessageSender = (*refMessageSender)(nil)

func (ms *refMessageSender) SendRequest(ctx context.Context, p peer.ID, req *refpb.Message) (*refpb.Message, error) {
	s, err := ms.host.NewStream(ctx, p, ms.protocolID)
	if err != nil {
		return nil, fmt.Errorf("stream creation: %w", err)
	}
	defer s.Close()

	if err := pbio.NewDelimitedWriter(s).WriteMsg(req); err != nil {
		_ = s.Reset()
		return nil, fmt.Errorf("write message: %w", err)
	}

	type readResult struct {
		data []byte
		err  error
	}

	// Read on a goroutine so a peer that never replies surfaces as the context
	// deadline rather than blocking the test indefinitely.
	resultc := make(chan readResult, 1)
	go func(r msgio.ReadCloser) {
		data, err := r.ReadMsg()
		resultc <- readResult{data: data, err: err}
	}(msgio.NewVarintReaderSize(s, network.MessageSizeMax))

	select {
	case <-ctx.Done():
		_ = s.Reset()
		return nil, fmt.Errorf("read message: %w", ctx.Err())
	case res := <-resultc:
		if res.err != nil {
			_ = s.Reset()
			return nil, fmt.Errorf("read message: %w", res.err)
		}

		resp := &refpb.Message{}
		if err := proto.Unmarshal(res.data, resp); err != nil {
			return nil, fmt.Errorf("unmarshal response: %w", err)
		}

		return resp, nil
	}
}

func (ms *refMessageSender) SendMessage(ctx context.Context, p peer.ID, req *refpb.Message) error {
	s, err := ms.host.NewStream(ctx, p, ms.protocolID)
	if err != nil {
		return fmt.Errorf("stream creation: %w", err)
	}
	defer s.Close()

	if err := pbio.NewDelimitedWriter(s).WriteMsg(req); err != nil {
		_ = s.Reset()
		return fmt.Errorf("write message: %w", err)
	}

	return nil
}

// newListeningHost returns a host with a loopback TCP listen address, so that
// the two implementations under test can dial each other.
func newListeningHost(t testing.TB) host.Host {
	t.Helper()
	return newTestHost(t, libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
}

// newInteropServerDHT returns a zikade DHT that listens for inbound streams.
func newInteropServerDHT(t testing.TB) *DHT {
	t.Helper()

	cfg := DefaultConfig()
	cfg.Logger = devnull

	// The default mode starts in client mode and only registers the stream
	// handler once it judges itself reachable, which never happens on loopback.
	cfg.Mode = ModeOptServer

	h := newListeningHost(t)

	d, err := New(h, cfg)
	if err != nil {
		t.Fatalf("new zikade dht: %v", err)
	}

	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Logf("closing dht: %s", err)
		}
		if err := h.Close(); err != nil {
			t.Logf("closing host: %s", err)
		}
	})

	return d
}

// newReferenceServer returns a go-libp2p-kad-dht node in server mode speaking
// the same protocol as [ProtocolIPFS], along with the host it runs on.
func newReferenceServer(t testing.TB) (*kaddht.IpfsDHT, host.Host) {
	t.Helper()

	h := newListeningHost(t)

	d, err := kaddht.New(h,
		kaddht.Mode(kaddht.ModeServer),
		kaddht.ProtocolPrefix("/ipfs"),
	)
	if err != nil {
		t.Fatalf("new reference dht: %v", err)
	}

	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Logf("closing reference dht: %s", err)
		}
		if err := h.Close(); err != nil {
			t.Logf("closing reference host: %s", err)
		}
	})

	return d, h
}

func connectHosts(t testing.TB, ctx context.Context, from, to host.Host) {
	t.Helper()

	addrInfo := peer.AddrInfo{ID: to.ID(), Addrs: to.Addrs()}
	if err := from.Connect(ctx, addrInfo); err != nil {
		t.Fatalf("connecting %s to %s: %v", from.ID(), to.ID(), err)
	}
}

// newInteropRouter builds a [router] on the given DHT's host.
func newInteropRouter(d *DHT) *router {
	return &router{
		host:       d.host,
		protocolID: d.cfg.ProtocolID,
		tele:       d.tele,
		clk:        d.cfg.Clock,
	}
}

// TestInteropPutValueFromReferenceClient checks that a go-libp2p-kad-dht client
// can store a record on a zikade server.
func TestInteropPutValueFromReferenceClient(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), interopTimeout)
	defer cancel()

	d := newInteropServerDHT(t)

	clientHost := newListeningHost(t)
	connectHosts(t, ctx, clientHost, d.host)

	pm, err := refpb.NewProtocolMessenger(&refMessageSender{
		host:       clientHost,
		protocolID: d.cfg.ProtocolID,
	})
	if err != nil {
		t.Fatalf("new protocol messenger: %v", err)
	}

	key, value := makePkKeyValue(t)

	if err := pm.PutValue(ctx, d.host.ID(), record.MakePutRecord(key, value)); err != nil {
		t.Fatalf("reference client putting value to zikade server: %v", err)
	}

	// Read the record back over the wire, rather than reaching into the
	// server's backend, so the check stays at the protocol level.
	rec, _, err := pm.GetValue(ctx, d.host.ID(), key)
	if err != nil {
		t.Fatalf("reference client getting value from zikade server: %v", err)
	}

	if rec == nil {
		t.Fatal("reference client got no record back from zikade server")
	}

	if !bytes.Equal(rec.GetValue(), value) {
		t.Errorf("record value mismatch: got %x, want %x", rec.GetValue(), value)
	}
}

// TestInteropPutValueToReferenceServer checks that a zikade client can store a record
// on a go-libp2p-kad-dht server.
func TestInteropPutValueToReferenceServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), interopTimeout)
	defer cancel()

	_, serverHost := newReferenceServer(t)

	d := newTestDHT(t)
	connectHosts(t, ctx, d.host, serverHost)

	rtr := newInteropRouter(d)
	to := kadt.PeerID(serverHost.ID())

	key, value := makePkKeyValue(t)

	resp, err := rtr.SendMessage(ctx, to, &pb.Message{
		Type:   pb.Message_PUT_VALUE,
		Key:    []byte(key),
		Record: record.MakePutRecord(key, value),
	})
	if err != nil {
		t.Fatalf("zikade client putting value to reference server: %v", err)
	}

	if resp == nil {
		t.Fatal("zikade client got no response to PUT_VALUE from reference server")
	}

	if !bytes.Equal(resp.GetRecord().GetValue(), value) {
		t.Errorf("echoed record value mismatch: got %x, want %x", resp.GetRecord().GetValue(), value)
	}

	// Confirm the server actually stored it, again over the wire.
	getResp, err := rtr.SendMessage(ctx, to, &pb.Message{
		Type: pb.Message_GET_VALUE,
		Key:  []byte(key),
	})
	if err != nil {
		t.Fatalf("zikade client getting value from reference server: %v", err)
	}

	if !bytes.Equal(getResp.GetRecord().GetValue(), value) {
		t.Errorf("stored record value mismatch: got %x, want %x", getResp.GetRecord().GetValue(), value)
	}
}

// TestInteropAddProviderFromReferenceClient checks that a go-libp2p-kad-dht
// client can register itself as a provider with a zikade server.
func TestInteropAddProviderFromReferenceClient(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), interopTimeout)
	defer cancel()

	d := newInteropServerDHT(t)

	clientHost := newListeningHost(t)
	connectHosts(t, ctx, clientHost, d.host)

	ms := &refMessageSender{host: clientHost, protocolID: d.cfg.ProtocolID}

	pm, err := refpb.NewProtocolMessenger(ms)
	if err != nil {
		t.Fatalf("new protocol messenger: %v", err)
	}

	c := newRandomContent(t)

	if err := pm.PutProvider(ctx, d.host.ID(), c.Hash(), clientHost); err != nil {
		t.Fatalf("reference client adding provider to zikade server: %v", err)
	}

	// ADD_PROVIDER draws no response, so the write succeeding proves little on
	// its own. GET_PROVIDERS is a request/response exchange, so it is what
	// catches a server that stored nothing.
	//
	// The reference message sender opens a stream per message, so the two travel
	// on separate streams and the server handles them on separate goroutines. A
	// single read can therefore overtake the write it is checking, which is why
	// this polls rather than reading once.
	deadline := time.Now().Add(interopSettleTimeout)
	for {
		provs, _, err := pm.GetProviders(ctx, d.host.ID(), c.Hash())
		if err != nil {
			t.Fatalf("reference client getting providers from zikade server: %v", err)
		}

		if slices.ContainsFunc(provs, func(p *peer.AddrInfo) bool { return p.ID == clientHost.ID() }) {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("provider %s not returned by zikade server within %s, got %v", clientHost.ID(), interopSettleTimeout, provs)
		}

		time.Sleep(interopPollInterval)
	}
}
