# Enable structured debug logging

Generated `ShardedQueries` methods can emit one structured slog record when a
routed query completes. Logging is disabled by default and is independent of
OpenTelemetry tracing and metrics.

Create a logger whose handler enables Debug records, then pass it when building
the topology:

```go
logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelDebug,
}))

mesh, err := pgmesh.CreateMesh(ctx, &pgmesh.Options[
    *db.ReadQueries,
    *db.StoreQueries,
    ShardKey,
]{
    // ReplicaSets, Shards, CreateNode, and ShardHasher omitted.
    Logger: logger,
})
```

`NewBuilder` users can call `WithLogger(logger)` instead. Passing nil disables
logging. pgmesh does not modify the logger or its handler; if the handler's
minimum level is higher than Debug, the records are filtered normally.

Every record has Debug level, the message `pgmesh query completed`, and these
structured attributes:

| Attribute | Value |
| --- | --- |
| `query_name` | Generated query method name |
| `query_kind` | `read` or `write` |
| `failed` | Boolean error outcome |
| `duration` | End-to-end duration as a `slog.Duration` value |
| `vshard` | Virtual shard index, encoded as a string |
| `replica_set` | Physical replica-set name |
| `route_mode` | `read`, `primary`, or `transaction` |
| `write_mirror_count` | Synchronous mirrors used by the operation |
| `error` | Error value, present only when the operation failed |

A routing failure is still logged but has no route attributes because no shard
was selected. The original query context is passed to `LogAttrs`, allowing a
context-aware slog handler or OpenTelemetry logging bridge to add trace
correlation fields.
