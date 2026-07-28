# Enable OpenTelemetry tracing and metrics

Generated `Store` methods create one internal span and record metrics for each
query, regardless of whether the configuration uses one database, replicas, or
shards.

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

store, err := db.NewStore(
    ctx,
    db.Singleton(pool),
    db.WithTracerProvider(tracerProvider),
    db.WithMeterProvider(meterProvider),
)
```

The store options apply to both singleton and sharded topologies. If providers
are not supplied, pgmesh uses OpenTelemetry's global providers, so applications that call
`otel.SetTracerProvider` and
`otel.SetMeterProvider` need no pgmesh-specific options. When no SDK is
configured, OpenTelemetry's default providers are no-ops. The application owns
provider lifecycle: build providers before the store, keep them alive while
queries run, then flush or shut them down after query traffic has stopped.
pgmesh never shuts down a provider.

pgmesh emits one metric instrument:

| Metric | Type | Unit | Meaning |
| --- | --- | --- | --- |
| `pgmesh.query.duration` | Histogram | `s` | End-to-end routed query duration |

Use the histogram's count to calculate query throughput and error rates. Keeping
count and latency in one instrument avoids duplicating metric streams. Its
default explicit bucket boundaries are `0.001`, `0.005`, `0.01`, `0.05`, `0.1`,
`0.5`, `1`, `5`, and `10` seconds; applications can override the aggregation
with an SDK view when their latency objectives need different boundaries.

The span name is `pgmesh.query.<SubStore>.<QueryName>`, using the query's
`store:` annotation. For example, `store: Users` on `CreateUser` produces
`pgmesh.query.Users.CreateUser`. Every span records the query name and kind.
The duration metric records the same bounded dimensions. Successfully routed
operations also record the selected physical route:

| Attribute | When recorded | Value |
| --- | --- | --- |
| `pgmesh.query.name` | All spans and metric points | Generated query method name |
| `pgmesh.query.kind` | All spans and metric points | `read` or `write` |
| `error.type` | Failed spans and metric points | Predictable Go error type |
| `pgmesh.route.replica_set` | Successfully routed spans and metric points | Physical replica-set name |
| `pgmesh.route.mode` | Successfully routed spans and metric points | `read`, `primary`, or `transaction` |

Virtual-shard indexes are deliberately excluded from OpenTelemetry attributes
because they create one dimension value per virtual shard. Debug logs retain
the selected `vshard` for individual-query diagnosis. Scatter and grouped-copy
operations retain one logical span for the generated method and omit a
misleading single virtual-shard or replica-set value.

Routing, database, and mirror errors are recorded on the span and set its status
to error. Successful operations omit `error.type`.

The span-derived context is passed into the selected generated query method. If
the pgx pool is instrumented separately, its database spans therefore appear as
children of the pgmesh routing span.
