package coord

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/probe-lab/zikade/tele"
)

// Telemetry is the struct that holds a reference to all metrics and the tracer used
// by the coordinator and its components.
// Make sure to also register the [MeterProviderOpts] with your custom or the global
// [metric.MeterProvider].
type Telemetry struct {
	Tracer trace.Tracer

	// eventLoopBusySeconds accumulates the time the event loop has spent performing work.
	eventLoopBusySeconds metric.Float64Counter

	// eventLoopPasses counts the passes the event loop has made.
	eventLoopPasses metric.Int64Counter
}

// NewTelemetry initializes a Telemetry struct with the given meter and tracer providers.
func NewTelemetry(meterProvider metric.MeterProvider, tracerProvider trace.TracerProvider) (*Telemetry, error) {
	meter := meterProvider.Meter(tele.MeterName)

	t := &Telemetry{
		Tracer: tracerProvider.Tracer(tele.TracerName),
	}

	var err error

	t.eventLoopBusySeconds, err = meter.Float64Counter(
		"coordinator_event_loop_busy_seconds",
		metric.WithDescription("Total time the coordinator's event loop has spent performing work"),
	)
	if err != nil {
		return nil, fmt.Errorf("create coordinator_event_loop_busy_seconds counter: %w", err)
	}

	t.eventLoopPasses, err = meter.Int64Counter(
		"coordinator_event_loop_passes",
		metric.WithDescription("Total number of passes the coordinator's event loop has made"),
	)
	if err != nil {
		return nil, fmt.Errorf("create coordinator_event_loop_passes counter: %w", err)
	}

	return t, nil
}

// RecordEventLoopPass records one pass of the coordinator's event loop and the time it spent
// working. The rate at which that time accumulates is the loop's occupancy: the fraction of
// wall clock time its single worker goroutine is unavailable to take on anything else.
func (t *Telemetry) RecordEventLoopPass(ctx context.Context, busy time.Duration) {
	t.eventLoopBusySeconds.Add(ctx, busy.Seconds())
	t.eventLoopPasses.Add(ctx, 1)
}
