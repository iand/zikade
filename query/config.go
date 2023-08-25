package query

import (
	"github.com/plprobelab/go-kademlia/query"
	"golang.org/x/exp/slog"

	"github.com/iand/zikade/internal/logger"
)

type Config struct {
	Pool   *query.PoolConfig
	Logger *slog.Logger // a structured logger that should be used when logging.
}

func DefaultConfig() *Config {
	return &Config{
		Pool:   query.DefaultPoolConfig(),
		Logger: logger.New("query"),
	}
}
