package logger

import (
	logging "github.com/ipfs/go-log/v2"
	"go.uber.org/zap/exp/zapslog"
	"golang.org/x/exp/slog"
)

func New(name string) *slog.Logger {
	return slog.New(zapslog.NewHandler(logging.Logger(name).Desugar().Core()))
}
