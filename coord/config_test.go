package coord

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfigValidate(t *testing.T) {
	t.Run("default is valid", func(t *testing.T) {
		cfg := DefaultConfig()
		require.NoError(t, cfg.Validate())
	})

	t.Run("clock is not nil", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Clock = nil
		require.Error(t, cfg.Validate())
	})

	t.Run("logger not nil", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Logger = nil
		require.Error(t, cfg.Validate())
	})
}
