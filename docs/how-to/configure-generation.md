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
      type: "StoreQueries"
      constructor: "NewStoreQueries"
      sql_package: "pgx/v5"
```

Include the other matching sqlc options used by your project.

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
      type: "StoreQueries"
      constructor: "NewStoreQueries"
      sql_package: "pgx/v5"
```

See [`integration/fixture/sqlc.yaml`](../../integration/fixture/sqlc.yaml) for
checked-in same-package and separate-package configurations that compile in CI.

## Customize generated names

The plugin supports options for interface, wrapper, constructor, resolver,
sharded facade, receiver, node constructor, and runtime import names. Prefer the
defaults until a collision or established package convention requires a
change. `runtime_import_path` is intended for forks of pgmesh.

`skip_with_tx` is unsupported because the runtime requires pgx/v5 transaction
support.

## Regenerate deterministically

In this repository:

```bash
just generate-example
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
