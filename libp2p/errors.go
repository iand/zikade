package libp2p

import "errors"

var (
	ErrUnknownPeer            = errors.New("unknown peer")
	ErrInvalidProtocol        = errors.New("invalid protocol")
	ErrInvalidPeer            = errors.New("invalid peer")
	ErrRequireProtoKadMessage = errors.New("require ProtoKadMessage")
	ErrInvalidRequestHandler  = errors.New("invalid request handler")
)
