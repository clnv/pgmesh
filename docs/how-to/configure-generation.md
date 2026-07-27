# Configure and regenerate code

## Keep the two generators aligned

sqlc generates the underlying models and query methods. pgmesh generates
wrappers that refer to those types. Options that affect signatures or names
must match in `gen.go` and the pgmesh `codegen.options` block.

Common paired options include:

- `package`;
- `sql_package`, which must be `pgx/v5`;
- `query_parameter_limit`;
- pointer-emission options;
- `rename` and `overrides`.

The repository's [`examples/sqlc/sqlc.yaml`](../../examples/sqlc/sqlc.yaml) is
a minimal same-package configuration.

## Generate into the same package

Set the sqlc Go output and plugin output to the same directory. The plugin can
refer to sqlc-generated types without an import:

```yaml
gen:
  go:
    package: "db"
    out: "internal/db"
codegen:
  - plugin: "pgmesh"
    out: "internal/db"
    options:
      package: "db"
      output_file_name: "zz_generated_store.go"
      sql_package: "pgx/v5"
```

Include the other matching sqlc options used by your project.

## Generated file layout

`output_file_name` names the combined store wrapper and supplies the stem for
the other generated files. For example, `zz_generated_store.go` produces:

- `zz_generated_store_interfaces.go` for the public `Store` and shard resolver contracts;
- `zz_generated_store_read.go` for the internal read executor;
- `zz_generated_store_write.go` for the internal write and mirror executor;
- `zz_generated_store.go` for query options, `DatabaseConfig`, `NewStore`, and the shared mesh executor;
- `zz_generated_store_sharded.go` for `ShardedConfig` and its topology builder.

The sharded file contains only the generated header and package clause when no
queries have shard routes. Keeping the complete file set makes regeneration
overwrite obsolete routed code after the last shard annotation is removed.

The public API is deliberately small:

| Symbol | Purpose |
| --- | --- |
| `Store` | Topology-independent generated query interface |
| `NewStore(ctx, config)` | The only store constructor |
| `Database(DatabaseConfig)` | Opaque single-primary, replica, and mirror configuration |
| `Sharded(ShardedConfig)` | Opaque sharded configuration, emitted only for sharded stores |
| `ReadFromPrimary`, `WithTx` | Query options shared by every topology |

Concrete readers, writers, database nodes, and routed implementations are
unexported generated details.

## Generate wrappers into a separate package

When the wrapper output differs from the sqlc Go output, tell the plugin how to
import the underlying package:

```yaml
gen:
  go:
    package: "internal"
    out: "internal/db"
codegen:
  - plugin: "pgmesh"
    out: "internal/store"
    options:
      package: "store"
      internal_import_path: "example.com/app/internal/db"
      internal_import_alias: "db"
      output_file_name: "zz_generated_store.go"
      sql_package: "pgx/v5"
```

See [`integration/fixture/sqlc.yaml`](../../integration/fixture/sqlc.yaml) for
checked-in same-package and separate-package configurations that compile in CI.

## Customize generated names

Prefer the stable `Store` and `NewStore` defaults so callers remain independent
of topology. `resolver_interface` can avoid a domain-name collision, and
`runtime_import_path` is intended for forks of pgmesh. Internal wrapper naming
options are retained for compatibility but should not be made part of
application code.

`skip_with_tx` is unsupported because the runtime requires pgx/v5 transaction
support.

## Regenerate deterministically

In this repository:

```bash
just --justfile examples/justfile generate
git diff --exit-code
```

In a downstream project:

```bash
sqlc generate
go test ./...
git diff --exit-code
```

Pin sqlc and the pgmesh plugin version in CI. Regenerate after changing SQL,
schema, annotations, output layout, renames, overrides, or pointer settings.
