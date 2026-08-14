package tiny_test

import (
	"github.com/probe-lab/zikade/internal/coord/coordt"
	"github.com/probe-lab/zikade/internal/tiny"
)

var _ coordt.Message[tiny.Key, tiny.Node] = tiny.Message{}
