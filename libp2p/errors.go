package libp2p

import "errors"

var (
	ErrNotPeerAddrInfo         = errors.New("not peer.AddrInfo")
	ErrUnknownPeer             = errors.New("unknown peer")
	ErrInvalidProtocol         = errors.New("invalid protocol")
	ErrInvalidPeer             = errors.New("invalid peer")
	ErrRequireProtoKadMessage  = errors.New("Libp2pEndpoint requires ProtoKadMessage")
	ErrRequireProtoKadResponse = errors.New("Libp2pEndpoint requires ProtoKadResponseMessage")
)
