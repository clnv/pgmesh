package pgmesh

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/clnv/pgmesh"

// OpenTelemetry metric instrument names emitted for routed queries.
const (
	MetricQueryCount    = "pgmesh.query.count"
	MetricQueryDuration = "pgmesh.query.duration"
)

// OpenTelemetry attribute keys recorded on routed query telemetry.
const (
	AttributeQueryName        = "pgmesh.query.name"
	AttributeQueryKind        = "pgmesh.query.kind"
	AttributeQueryError       = "pgmesh.query.error"
	AttributeVShard           = "pgmesh.route.vshard"
	AttributeReplicaSet       = "pgmesh.route.replica_set"
	AttributeRouteMode        = "pgmesh.route.mode"
	AttributeWriteMirrorCount = "pgmesh.route.write_mirror_count"
)

// QueryKind classifies a routed query as a read or write.
type QueryKind string

// Query kinds recorded by generated routed query methods.
const (
	QueryKindRead  QueryKind = "read"
	QueryKindWrite QueryKind = "write"
)

// RouteMode describes the database path selected for a routed query.
type RouteMode string

// Route modes recorded after a query resolves to a shard.
const (
	RouteModeRead        RouteMode = "read"
	RouteModePrimary     RouteMode = "primary"
	RouteModeTransaction RouteMode = "transaction"
)

type queryTelemetry struct {
	tracer        trace.Tracer
	queryCount    metric.Int64Counter
	queryDuration metric.Float64Histogram
	logger        *slog.Logger
}

// QueryTrace tracks telemetry for one routed query.
type QueryTrace struct {
	ctx           context.Context
	span          trace.Span
	queryCount    metric.Int64Counter
	queryDuration metric.Float64Histogram
	started       time.Time
	attributes    []attribute.KeyValue
	logger        *slog.Logger
	logAttributes []slog.Attr
}

func newQueryTelemetry(
	tracerProvider trace.TracerProvider,
	meterProvider metric.MeterProvider,
) (queryTelemetry, error) {
	var telemetry queryTelemetry
	telemetry.setTracerProvider(tracerProvider)
	if err := telemetry.setMeterProvider(meterProvider); err != nil {
		return queryTelemetry{}, err
	}
	return telemetry, nil
}

func (t *queryTelemetry) setTracerProvider(provider trace.TracerProvider) {
	if provider == nil {
		provider = otel.GetTracerProvider()
	}
	t.tracer = provider.Tracer(instrumentationName)
}

func (t *queryTelemetry) setMeterProvider(provider metric.MeterProvider) error {
	if provider == nil {
		provider = otel.GetMeterProvider()
	}
	meter := provider.Meter(instrumentationName)
	queryCount, err := meter.Int64Counter(
		MetricQueryCount,
		metric.WithDescription("Number of routed pgmesh queries"),
		metric.WithUnit("{query}"),
	)
	if err != nil {
		return err
	}
	queryDuration, err := meter.Float64Histogram(
		MetricQueryDuration,
		metric.WithDescription("Duration of routed pgmesh queries"),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(
			0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5,
			0.75, 1, 2.5, 5, 7.5, 10,
		),
	)
	if err != nil {
		return err
	}
	t.queryCount = queryCount
	t.queryDuration = queryDuration
	return nil
}

// StartQueryTrace starts telemetry for a routed query and returns the span
// context so database instrumentation can create child spans.
//
//nolint:spancheck // The generated caller ends the returned QueryTrace.
func (m *Mesh[R, W, SK]) StartQueryTrace(
	ctx context.Context,
	queryName string,
	kind QueryKind,
) (context.Context, *QueryTrace) {
	attributes := []attribute.KeyValue{
		attribute.String(AttributeQueryName, queryName),
		attribute.String(AttributeQueryKind, string(kind)),
	}
	ctx, span := m.telemetry.tracer.Start(
		ctx,
		"pgmesh.query "+queryName,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attributes...),
	)
	return ctx, &QueryTrace{
		ctx:           ctx,
		span:          span,
		queryCount:    m.telemetry.queryCount,
		queryDuration: m.telemetry.queryDuration,
		started:       time.Now(),
		attributes:    attributes,
		logger:        m.telemetry.logger,
		logAttributes: []slog.Attr{
			slog.String("query_name", queryName),
			slog.String("query_kind", string(kind)),
		},
	}
}

// SetRoute records the selected virtual shard, replica set, route mode, and
// synchronous mirror count.
func (t *QueryTrace) SetRoute(
	vshard uint64,
	replicaSet string,
	mode RouteMode,
	writeMirrorCount int,
) {
	routeAttributes := []attribute.KeyValue{
		attribute.String(AttributeVShard, strconv.FormatUint(vshard, 10)),
		attribute.String(AttributeReplicaSet, replicaSet),
		attribute.String(AttributeRouteMode, string(mode)),
		attribute.Int(AttributeWriteMirrorCount, writeMirrorCount),
	}
	t.span.SetAttributes(routeAttributes...)
	t.attributes = append(t.attributes, routeAttributes...)
	t.logAttributes = append(
		t.logAttributes,
		slog.String("vshard", strconv.FormatUint(vshard, 10)),
		slog.String("replica_set", replicaSet),
		slog.String("route_mode", string(mode)),
		slog.Int("write_mirror_count", writeMirrorCount),
	)
}

// End records metrics and a debug log, records err if present, then ends the
// routed query span.
func (t *QueryTrace) End(err error) {
	duration := time.Since(t.started)
	if err != nil {
		t.span.RecordError(err)
		t.span.SetStatus(codes.Error, err.Error())
	}
	metricAttributes := append(
		append([]attribute.KeyValue(nil), t.attributes...),
		attribute.Bool(AttributeQueryError, err != nil),
	)
	recordOptions := metric.WithAttributes(metricAttributes...)
	t.queryCount.Add(t.ctx, 1, recordOptions)
	t.queryDuration.Record(t.ctx, duration.Seconds(), recordOptions)
	if t.logger != nil && t.logger.Enabled(t.ctx, slog.LevelDebug) {
		logAttributes := append(
			append([]slog.Attr(nil), t.logAttributes...),
			slog.Bool("failed", err != nil),
			slog.Duration("duration", duration),
		)
		if err != nil {
			logAttributes = append(logAttributes, slog.Any("error", err))
		}
		t.logger.LogAttrs(t.ctx, slog.LevelDebug, "pgmesh query completed", logAttributes...)
	}
	t.span.End()
}
