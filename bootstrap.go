package zikade

import (
	"github.com/libp2p/go-libp2p/core/peer"
)

// defaultBootstrapPeers is a set of hard-coded public DHT bootstrap peers
// operated by Protocol Labs. This slice is filled in the init() method.
//
// The list mirrors the Amino DHT bootstrappers published at
// https://conf.ipfs-mainnet.org/autoconf.json, which is where Kubo now takes
// them from and which is refreshed daily. A hard-coded copy goes stale as
// bootstrappers are added and retired, so it is worth comparing against that
// document from time to time.
var defaultBootstrapPeers []peer.AddrInfo

func init() {
	// index records where a peer's entry sits in defaultBootstrapPeers so that a peer
	// listed under more than one address keeps its position in the list.
	index := make(map[peer.ID]int)

	for _, s := range []string{
		"/dnsaddr/bootstrap.libp2p.io/p2p/QmNnooDu7bfjPFoTZYxMNLWUQJyrVwtbZg5gBMjTezGAJN",
		"/dnsaddr/bootstrap.libp2p.io/p2p/QmQCU2EcMqAqQPR2i9bChDtGNJchTbq5TbXJJ16u19uLTa",
		"/dnsaddr/bootstrap.libp2p.io/p2p/QmbLHAnMoJPWSCR5Zhtx6BHJX9KiKNN6tpvbUcqanj75Nb",
		"/dnsaddr/bootstrap.libp2p.io/p2p/QmcZf59bWwK5XFi76CZX8cbJ4BhTzzA3gU1ZjYZcYW3dwt",
		"/dnsaddr/va1.bootstrap.libp2p.io/p2p/12D3KooWKnDdG3iXw9eTFijk3EWSunZcFi54Zka4wmtqtt6rPxc8",
		"/ip4/104.131.131.82/tcp/4001/p2p/QmaCpDMGvV2BGHeYERUEnRQAwe3N8SzbUtfsmvsqQLuvuJ",         // mars.i.ipfs.io
		"/ip4/104.131.131.82/udp/4001/quic-v1/p2p/QmaCpDMGvV2BGHeYERUEnRQAwe3N8SzbUtfsmvsqQLuvuJ", // mars.i.ipfs.io
	} {
		addrInfo, err := peer.AddrInfoFromString(s)
		if err != nil {
			panic(err)
		}

		// A peer reachable over several transports is listed once per address.
		// Bootstrapping wants one entry per peer, since the addresses are only
		// ever put in the peerstore and the ids alone seed the routing table.
		if i, ok := index[addrInfo.ID]; ok {
			defaultBootstrapPeers[i].Addrs = append(defaultBootstrapPeers[i].Addrs, addrInfo.Addrs...)
			continue
		}

		index[addrInfo.ID] = len(defaultBootstrapPeers)
		defaultBootstrapPeers = append(defaultBootstrapPeers, *addrInfo)
	}
}

// DefaultBootstrapPeers returns hard-coded public DHT bootstrap peers operated
// by Protocol Labs. You can configure your own set of bootstrap peers by
// overwriting the corresponding Config field.
func DefaultBootstrapPeers() []peer.AddrInfo {
	peers := make([]peer.AddrInfo, len(defaultBootstrapPeers))
	copy(peers, defaultBootstrapPeers)
	return peers
}
