package zikade

import (
	"context"
	"fmt"

	"github.com/iand/xorbie/routing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/probe-lab/zikade/kadt"
	"github.com/probe-lab/zikade/tele"
)

// Telemetry is the struct that holds a reference to all metrics and the tracer.
// Initialize this struct with [NewTelemetry]. Make sure
// to also register the [MeterProviderOpts] with your custom or the global
// [metric.MeterProvider].
//
// To see the documentation for each metric below, check out [NewTelemetry] and the
// metric.WithDescription() calls when initializing each metric.
type Telemetry struct {
	Tracer                 trace.Tracer
	ReceivedMessages       metric.Int64Counter
	ReceivedMessageErrors  metric.Int64Counter
	ReceivedBytes          metric.Int64Histogram
	InboundRequestLatency  metric.Float64Histogram
	OutboundRequestLatency metric.Float64Histogram
	SentMessages           metric.Int64Counter // number of messages sent that did not expect a response
	SentMessageErrors      metric.Int64Counter
	SentRequests           metric.Int64Counter // number of messages sent that expected a response
	SentRequestErrors      metric.Int64Counter
	SentBytes              metric.Int64Histogram
	LRUCache               metric.Int64Counter
}

// NewWithGlobalProviders uses the global meter and tracer providers from
// opentelemetry. Check out the documentation of [MeterProviderOpts] for
// implications of using this constructor.
func NewWithGlobalProviders() (*Telemetry, error) {
	return NewTelemetry(otel.GetMeterProvider(), otel.GetTracerProvider())
}

// NewTelemetry initializes a Telemetry struct with the given meter and tracer providers.
// It constructs the different metric counters and histograms. The histograms
// have custom boundaries. Therefore, the given [metric.MeterProvider] should
// have the custom view registered that [MeterProviderOpts] returns.
func NewTelemetry(meterProvider metric.MeterProvider, tracerProvider trace.TracerProvider) (*Telemetry, error) {
	var err error

	if meterProvider == nil {
		meterProvider = otel.GetMeterProvider()
	}

	if tracerProvider == nil {
		tracerProvider = otel.GetTracerProvider()
	}

	t := &Telemetry{
		Tracer: tracerProvider.Tracer(tele.TracerName),
	}

	meter := meterProvider.Meter(tele.MeterName)

	// Initalize metrics for the DHT

	t.ReceivedMessages, err = meter.Int64Counter("received_messages", metric.WithDescription("Total number of messages received per RPC"))
	if err != nil {
		return nil, fmt.Errorf("received_messages counter: %w", err)
	}

	t.ReceivedMessageErrors, err = meter.Int64Counter("received_message_errors", metric.WithDescription("Total number of errors for messages received per RPC"))
	if err != nil {
		return nil, fmt.Errorf("received_message_errors counter: %w", err)
	}

	t.ReceivedBytes, err = meter.Int64Histogram("received_bytes", metric.WithDescription("Total received bytes per RPC"), metric.WithUnit("By"))
	if err != nil {
		return nil, fmt.Errorf("received_bytes histogram: %w", err)
	}

	t.InboundRequestLatency, err = meter.Float64Histogram("inbound_request_latency", metric.WithDescription("Latency per RPC"), metric.WithUnit("ms"))
	if err != nil {
		return nil, fmt.Errorf("inbound_request_latency histogram: %w", err)
	}

	t.OutboundRequestLatency, err = meter.Float64Histogram("outbound_request_latency", metric.WithDescription("Latency per RPC"), metric.WithUnit("ms"))
	if err != nil {
		return nil, fmt.Errorf("outbound_request_latency histogram: %w", err)
	}

	t.SentMessages, err = meter.Int64Counter("sent_messages", metric.WithDescription("Total number of messages sent per RPC"))
	if err != nil {
		return nil, fmt.Errorf("sent_messages counter: %w", err)
	}

	t.SentMessageErrors, err = meter.Int64Counter("sent_message_errors", metric.WithDescription("Total number of errors for messages sent per RPC"))
	if err != nil {
		return nil, fmt.Errorf("sent_message_errors counter: %w", err)
	}

	t.SentRequests, err = meter.Int64Counter("sent_requests", metric.WithDescription("Total number of requests sent per RPC"))
	if err != nil {
		return nil, fmt.Errorf("sent_requests counter: %w", err)
	}

	t.SentRequestErrors, err = meter.Int64Counter("sent_request_errors", metric.WithDescription("Total number of errors for requests sent per RPC"))
	if err != nil {
		return nil, fmt.Errorf("sent_request_errors counter: %w", err)
	}

	t.SentBytes, err = meter.Int64Histogram("sent_bytes", metric.WithDescription("Total sent bytes per RPC"), metric.WithUnit("By"))
	if err != nil {
		return nil, fmt.Errorf("sent_bytes histogram: %w", err)
	}

	t.LRUCache, err = meter.Int64Counter("lru_cache", metric.WithDescription("Cache hit or miss counter"))
	if err != nil {
		return nil, fmt.Errorf("lru_cache counter: %w", err)
	}

	return t, nil
}

// registerRoutingTableMetrics registers observable gauges that report the routing table's size, how
// many buckets are occupied, and the size of each occupied bucket tagged by common prefix length.
func registerRoutingTableMetrics(meterProvider metric.MeterProvider, rt routing.RoutingTableCpl[kadt.Key, kadt.PeerID]) error {
	if meterProvider == nil {
		meterProvider = otel.GetMeterProvider()
	}
	meter := meterProvider.Meter(tele.MeterName)

	var zeroKey kadt.Key
	bits := zeroKey.BitLen()

	_, err := meter.Int64ObservableGauge(
		"routing_table_size",
		metric.WithDescription("Number of nodes in the routing table"),
		metric.WithInt64Callback(func(ctx context.Context, o metric.Int64Observer) error {
			if sizer, ok := rt.(interface{ Size() int }); ok {
				o.Observe(int64(sizer.Size()))
			}
			return nil
		}),
	)
	if err != nil {
		return fmt.Errorf("routing_table_size gauge: %w", err)
	}

	_, err = meter.Int64ObservableGauge(
		"routing_table_buckets_occupied",
		metric.WithDescription("Number of common prefix lengths holding at least one node"),
		metric.WithInt64Callback(func(ctx context.Context, o metric.Int64Observer) error {
			var n int64
			for cpl := range bits {
				if rt.CplSize(cpl) > 0 {
					n++
				}
			}
			o.Observe(n)
			return nil
		}),
	)
	if err != nil {
		return fmt.Errorf("routing_table_buckets_occupied gauge: %w", err)
	}

	_, err = meter.Int64ObservableGauge(
		"routing_table_bucket_size",
		metric.WithDescription("Number of nodes in each occupied bucket, tagged by common prefix length"),
		metric.WithInt64Callback(func(ctx context.Context, o metric.Int64Observer) error {
			for cpl := range bits {
				if size := rt.CplSize(cpl); size > 0 {
					o.Observe(int64(size), metric.WithAttributes(attribute.Int("cpl", cpl)))
				}
			}
			return nil
		}),
	)
	if err != nil {
		return fmt.Errorf("routing_table_bucket_size gauge: %w", err)
	}

	return nil
}
