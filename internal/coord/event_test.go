package coord

import (
	"github.com/probe-lab/zikade/kadt"
	"github.com/probe-lab/zikade/pb"
)

var _ NetworkCommand = (*EventOutboundGetCloserNodes[kadt.Key, kadt.PeerID])(nil)

var (
	_ RoutingCommand = (*EventAddNode[kadt.Key, kadt.PeerID])(nil)
	_ RoutingCommand = (*EventStartBootstrap[kadt.Key, kadt.PeerID])(nil)
)

var (
	_ QueryCommand = (*EventStartMessageQuery[kadt.Key, kadt.PeerID, *pb.Message])(nil)
	_ QueryCommand = (*EventStartFindCloserQuery[kadt.Key, kadt.PeerID, *pb.Message])(nil)
	_ QueryCommand = (*EventStopQuery)(nil)
)

var (
	_ RoutingNotification = (*EventRoutingUpdated[kadt.Key, kadt.PeerID])(nil)
	_ RoutingNotification = (*EventBootstrapFinished)(nil)
)

var _ NodeHandlerRequest = (*EventOutboundGetCloserNodes[kadt.Key, kadt.PeerID])(nil)

var (
	_ NodeHandlerResponse = (*EventGetCloserNodesSuccess[kadt.Key, kadt.PeerID])(nil)
	_ NodeHandlerResponse = (*EventGetCloserNodesFailure[kadt.Key, kadt.PeerID])(nil)
)
