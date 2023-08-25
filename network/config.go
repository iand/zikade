package network

import (
	"golang.org/x/exp/slog"

	"github.com/iand/zikade/internal/logger"
)

type Config struct {
	Logger *slog.Logger // a structured logger that should be used when logging.
}

func DefaultConfig() *Config {
	return &Config{
		Logger: logger.New("network"),
	}
}
