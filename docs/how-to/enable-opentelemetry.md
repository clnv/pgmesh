# Enable OpenTelemetry tracing and metrics

Generated `ShardedQueries` methods create one internal span and record metrics
for each routed query. Node-level `ReadQueries`, `WriteQueries`, and
`StoreQueries` methods do not create pgmesh telemetry because they do not
perform mesh routing.

Configure trace and metric SDKs and exporters in the application, then pass
their providers when building the topology:

```go
tracerProvider := sdktrace.NewTracerProvider(
    sdktrace.WithBatcher(traceExporter),
    sdktrace.WithResource(resource),
)
defer tracerProvider.Shutdown(context.Background())

meterProvider := sdkmetric.NewMeterProvider(
    sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
    sdkmetric.WithResource(resource),
)
defer meterProvider.Shutdown(context.Background())

mesh, err := pgmesh.CreateMesh(ctx, &pgmesh.Options[
    *db.ReadQueries,
    *db.StoreQueries,
    ShardKey,
]{
    // ReplicaSets, Shards, CreateNode, and ShardHasher omitted.
    TracerProvider: tracerProvider,
    MeterProvider:  meterProvider,
})
```

`NewBuilder` users can call `WithTracerProvider` and `WithMeterProvider`
instead. If providers are not supplied, pgmesh uses OpenTelemetry's global
providers, so applications that call `otel.SetTracerProvider` and
`otel.SetMeterProvider` need no pgmesh-specific options. When no SDK is
configured, OpenTelemetry's default providers are no-ops.

pgmesh emits two metric instruments:

| Metric | Type | Unit | Meaning |
| --- | --- | --- | --- |
| `pgmesh.query.count` | Counter | `{query}` | Number of completed routed queries |
| `pgmesh.query.duration` | Histogram | `s` | End-to-end routed query duration |

The span name is `pgmesh.query <QueryName>`. Every span records the query name
and kind. Metrics record the same dimensions plus the error outcome;
successfully routed operations also record the selected route:

| Attribute | Value |
| --- | --- |
| `pgmesh.query.name` | Generated query method name |
| `pgmesh.query.kind` | `read` or `write` |
| `pgmesh.query.error` | Metric-only boolean error outcome |
| `pgmesh.route.vshard` | Virtual shard index, encoded as a string |
| `pgmesh.route.replica_set` | Physical replica-set name |
| `pgmesh.route.mode` | `read`, `primary`, or `transaction` |
| `pgmesh.route.write_mirror_count` | Synchronous mirrors used by the operation |

Routing, database, and mirror errors are recorded on the span and set its status
to error. Transaction-bound operations report zero write mirrors because
transactions deliberately drop cross-database mirrors.

The span-derived context is passed into the selected generated query method. If
the pgx pool is instrumented separately, its database spans therefore appear as
children of the pgmesh routing span.
