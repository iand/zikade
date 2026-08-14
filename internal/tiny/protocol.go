package tiny

import (
	"fmt"
	"math/bits"
)

// A Protocol supplies the node ids and the messages a tiny test network needs. It carries
// the state needed to mint distinct node ids, so a network is given one of its own.
type Protocol struct {
	minted int
}

func NewProtocol() *Protocol {
	return &Protocol{}
}

// NewNodeID returns a node whose key differs from every key it has already returned. The
// keys are counter values with their bits reversed, so nodes minted in sequence are spread
// across the key space instead of sharing a long prefix.
func (p *Protocol) NewNodeID() (Node, error) {
	if p.minted > 0xff {
		return Node{}, fmt.Errorf("no key left for a %dth node", p.minted+1)
	}

	n := NewNode(Key(bits.Reverse8(uint8(p.minted))))
	p.minted++

	return n, nil
}

// FindRequest returns a message asking for the nodes closest to target.
func (p *Protocol) FindRequest(target Key) Message {
	return Message{Content: "find", TargetKey: target}
}

// Reply returns the response to req, echoing its content and target the way a real protocol
// would identify the request being answered.
func (p *Protocol) Reply(req Message, nodes []Node) Message {
	return Message{
		Content:   req.Content,
		TargetKey: req.TargetKey,
		Closer:    nodes,
	}
}
