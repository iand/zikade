package zikade

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	record "github.com/libp2p/go-libp2p-record"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/probe-lab/zikade/internal/kadtest"
	"github.com/probe-lab/zikade/kadt"
	"github.com/probe-lab/zikade/pb"
)

func TestNew(t *testing.T) {
	h := newTestHost(t, libp2p.NoListenAddrs)

	tests := []struct {
		name        string
		cfgBuilder  func(*Config) *Config
		wantBuilder func(*DHT) *DHT
		wantErr     bool
	}{
		{
			name: "mode set to server",
			cfgBuilder: func(c *Config) *Config {
				c.Mode = ModeOptServer
				return c
			},
			wantBuilder: func(dht *DHT) *DHT {
				dht.mode = modeServer
				return dht
			},
			wantErr: false,
		},
		{
			name: "mode set to auto client",
			cfgBuilder: func(c *Config) *Config {
				c.Mode = ModeOptAutoClient
				return c
			},
			wantBuilder: func(dht *DHT) *DHT {
				dht.mode = modeClient
				return dht
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := DefaultConfig()
			d, err := New(h, c)
			if err != nil {
				t.Fatal(err)
			}

			got, err := New(h, tt.cfgBuilder(c))
			if (err != nil) != tt.wantErr {
				t.Errorf("New() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			want := tt.wantBuilder(d)

			assert.Equal(t, want.mode, got.mode)
		})
	}
}

func TestAddAddresses(t *testing.T) {
	ctx := kadtest.CtxShort(t)

	top := NewTopology(t)
	local := top.AddClient(nil)
	remote := top.AddServer(nil)

	// local routing table should not contain the node
	require.False(t, local.kad.IsRoutable(ctx, kadt.PeerID(remote.host.ID())))

	remoteAddrInfo := peer.AddrInfo{
		ID:    remote.host.ID(),
		Addrs: remote.host.Addrs(),
	}
	require.NotEmpty(t, remoteAddrInfo.ID)
	require.NotEmpty(t, remoteAddrInfo.Addrs)

	// Add remote's addresss to the local dht
	err := local.AddAddresses(ctx, []peer.AddrInfo{remoteAddrInfo}, time.Minute)
	require.NoError(t, err)

	// the include state machine runs in the background and eventually should add the node to routing table
	_, err = top.ExpectRoutingUpdated(ctx, local, remote.host.ID())
	require.NoError(t, err)

	// the routing table should now contain the node
	require.True(t, local.kad.IsRoutable(ctx, kadt.PeerID(remote.host.ID())))
}

func TestDHT_Close_idempotent(t *testing.T) {
	d := newTestDHT(t)

	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			assert.NoError(t, d.Close())
		})
	}
	wg.Wait()
}

// TestDHT_bootstrap_peer_addresses_known_at_startup checks that the addresses of the
// configured bootstrap peers are in the peerstore once the DHT is built, since it may
// bootstrap from them before anything asks it to.
func TestDHT_bootstrap_peer_addresses_known_at_startup(t *testing.T) {
	top := NewTopology(t)
	remote := top.AddServer(nil)

	cfg := DefaultConfig()
	cfg.BootstrapPeers = []peer.AddrInfo{
		{ID: remote.host.ID(), Addrs: remote.host.Addrs()},
	}
	local := top.AddServer(cfg)

	require.NotEmpty(t, local.host.Peerstore().Addrs(remote.host.ID()))
}

func TestSendMessage(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	serverCfg := DefaultConfig()
	serverCfg.Logger = devnull
	serverCfg.Mode = ModeOptServer
	serverHost := newTestHost(t, libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	server, err := New(serverHost, serverCfg)
	if err != nil {
		t.Fatalf("new server dht: %v", err)
	}
	t.Cleanup(func() {
		_ = server.Close()
		_ = serverHost.Close()
	})

	client := newTestDHT(t)
	if err := client.host.Connect(ctx, peer.AddrInfo{ID: serverHost.ID(), Addrs: serverHost.Addrs()}); err != nil {
		t.Fatalf("connect client to server: %v", err)
	}

	key, value := makePkKeyValue(t)

	testCases := []struct {
		name    string
		to      peer.ID
		req     *pb.Message
		wantErr bool
		check   func(t *testing.T, resp *pb.Message)
	}{
		{
			name: "find_node",
			to:   serverHost.ID(),
			req:  &pb.Message{Type: pb.Message_FIND_NODE, Key: []byte(newPeerID(t))},
			check: func(t *testing.T, resp *pb.Message) {
				if resp.GetType() != pb.Message_FIND_NODE {
					t.Errorf("got response type %s, want FIND_NODE", resp.GetType())
				}
			},
		},
		{
			name: "put_value_echoes_record",
			to:   serverHost.ID(),
			req: &pb.Message{
				Type:   pb.Message_PUT_VALUE,
				Key:    []byte(key),
				Record: record.MakePutRecord(key, value),
			},
			check: func(t *testing.T, resp *pb.Message) {
				if !bytes.Equal(resp.GetRecord().GetValue(), value) {
					t.Errorf("got echoed value %x, want %x", resp.GetRecord().GetValue(), value)
				}
			},
		},
		{
			name: "add_provider_draws_no_response",
			to:   serverHost.ID(),
			req:  newAddProviderRequest(newRandomContent(t).Hash(), peer.AddrInfo{ID: client.host.ID(), Addrs: client.host.Addrs()}),
			check: func(t *testing.T, resp *pb.Message) {
				if resp != nil {
					t.Errorf("got response %v, want nil", resp)
				}
			},
		},
		{
			name:    "peer_without_address",
			to:      newPeerID(t),
			req:     &pb.Message{Type: pb.Message_PING},
			wantErr: true,
		},
		{
			name:    "nil_request",
			to:      serverHost.ID(),
			req:     nil,
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := client.SendMessage(ctx, tc.to, tc.req)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("got no error, want one")
				}
				return
			}
			if err != nil {
				t.Fatalf("send message: %v", err)
			}
			tc.check(t, resp)
		})
	}
}
