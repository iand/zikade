package coord

import (
	"fmt"

	"github.com/benbjohnson/clock"
	"github.com/plprobelab/go-kademlia/kaderr"
	"golang.org/x/exp/slog"

	"github.com/iand/zikade/internal/logger"
)

type Config struct {
	Clock clock.Clock // a clock that may replaced by a mock when testing

	Logger *slog.Logger // a structured logger that should be used when logging.
}

// Validate checks the configuration options and returns an error if any have invalid values.
func (cfg *Config) Validate() error {
	if cfg.Clock == nil {
		return &kaderr.ConfigurationError{
			Component: "DhtConfig",
			Err:       fmt.Errorf("clock must not be nil"),
		}
	}

	if cfg.Logger == nil {
		return &kaderr.ConfigurationError{
			Component: "DhtConfig",
			Err:       fmt.Errorf("logger must not be nil"),
		}
	}
	return nil
}

func DefaultConfig() *Config {
	return &Config{
		Clock:  clock.New(), // use standard time
		Logger: logger.New("coord"),
	}
}
