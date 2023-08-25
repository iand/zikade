package routing

import (
	"github.com/plprobelab/go-kademlia/kad"
	"github.com/plprobelab/go-kademlia/routing"
	"golang.org/x/exp/slog"

	"github.com/iand/zikade/internal/logger"
)

// TODO: routing.BootstrapConfig declares generic parameters but doesn't actually use them, they can be removed
type Config[K kad.Key[K], A kad.Address[A]] struct {
	Bootstrap *routing.BootstrapConfig[K, A]
	Include   *routing.IncludeConfig
	Logger    *slog.Logger // a structured logger that should be used when logging.
}

func DefaultConfig[K kad.Key[K], A kad.Address[A]]() *Config[K, A] {
	return &Config[K, A]{
		Bootstrap: routing.DefaultBootstrapConfig[K, A](),
		Include:   routing.DefaultIncludeConfig(),
		Logger:    logger.New("query"),
	}
}
