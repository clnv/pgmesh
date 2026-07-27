package sqlcplugin

import (
	"bytes"
	"fmt"
	"go/format"
	"path/filepath"
	"strings"

	"github.com/sqlc-dev/plugin-sdk-go/plugin"
)

func generateWrapper(opts *options, queries []generatedQuery, imports *importSet) ([]*plugin.File, error) {
	routes, err := collectShardRoutes(queries)
	if err != nil {
		return nil, err
	}
	if len(routes) > 0 {
		for _, query := range queries {
			if query.route == nil {
				return nil, fmt.Errorf(
					"query %s must declare shard metadata because this store contains sharded queries; move unsharded queries to another generated store",
					query.methodName,
				)
			}
		}
	}
	if opts.InternalImportPath != "" {
		imports.addNamed(importAlias(opts.InternalImportAlias), opts.InternalImportPath)
	}
	imports.addNamed(defaultRuntimeAlias, opts.RuntimeImportPath)
	imports.add(defaultContext)
	imports.add(defaultFMT)
	imports.add(defaultSlog)
	imports.add(defaultMetric)
	imports.add(defaultTrace)
	imports.add(defaultPGXPackage)
	if !opts.IgnoreMirrorError {
		imports.add(defaultDatabaseSQL)
		imports.add(defaultErrorsPackage)
	}

	files := make([]*plugin.File, 0, 5)
	appendFile := func(name string, writeBody func(*bytes.Buffer)) error {
		content, generateErr := generateFile(opts, imports, writeBody)
		if generateErr != nil {
			return generateErr
		}
		files = append(files, &plugin.File{Name: name, Contents: content})
		return nil
	}

	if err := appendFile(derivedOutputFileName(opts.OutputFileName, "interfaces"), func(out *bytes.Buffer) {
		writeQueryInterfaces(out, opts, queries)
		if len(routes) > 0 {
			writeShardResolverInterface(out, opts, routes)
		}
	}); err != nil {
		return nil, err
	}
	if err := appendFile(derivedOutputFileName(opts.OutputFileName, "read"), func(out *bytes.Buffer) {
		writeReadQueries(out, opts, queries)
	}); err != nil {
		return nil, err
	}
	if err := appendFile(derivedOutputFileName(opts.OutputFileName, "write"), func(out *bytes.Buffer) {
		writeWriteQueries(out, opts, queries)
	}); err != nil {
		return nil, err
	}
	if err := appendFile(opts.OutputFileName, func(out *bytes.Buffer) {
		writeQueryOptions(out)
		writeQueryStore(out, opts)
		writeNodeConstructor(out, opts)
		writeStoreConfiguration(out, opts, queries)
	}); err != nil {
		return nil, err
	}
	if err := appendFile(derivedOutputFileName(opts.OutputFileName, "sharded"), func(out *bytes.Buffer) {
		if len(routes) > 0 {
			writeShardedStore(out, opts)
		}
	}); err != nil {
		return nil, err
	}

	return files, nil
}

func generateFile(opts *options, imports *importSet, writeBody func(*bytes.Buffer)) ([]byte, error) {
	var body bytes.Buffer
	writeBody(&body)

	var out bytes.Buffer
	fmt.Fprintf(&out, "%s\n\n", generatedHeader)
	fmt.Fprintf(&out, "package %s\n\n", opts.PackageName)
	writeImports(&out, usedImports(imports.sorted(), body.String()))
	out.Write(body.Bytes())

	formatted, err := format.Source(out.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generated wrapper: %w", err)
	}
	return formatted, nil
}

func derivedOutputFileName(outputFileName, section string) string {
	extension := filepath.Ext(outputFileName)
	stem := strings.TrimSuffix(outputFileName, extension)
	return stem + "_" + section + extension
}

func usedImports(imports []importSpec, body string) []importSpec {
	used := make([]importSpec, 0, len(imports))
	for _, imp := range imports {
		qualifier := imp.name
		if qualifier == "" {
			qualifier = packageNameForImport(imp.path)
		}
		if strings.Contains(body, qualifier+".") {
			used = append(used, imp)
		}
	}
	return used
}

func writeQueryInterfaces(out *bytes.Buffer, opts *options, queries []generatedQuery) {
	writeSplitInterface(out, defaultReadInterface, queries, queryKindRead)
	writeSplitInterface(out, defaultWriteInterface, queries, queryKindWrite)
	fmt.Fprintf(out, "// %s is the topology-independent generated query API.\n", opts.StoreInterfaceName)
	fmt.Fprintf(out, "type %s interface {\n", opts.StoreInterfaceName)
	for _, query := range queries {
		fmt.Fprintf(out, "\t// %s executes the generated %s query.\n", query.methodName, query.methodName)
		fmt.Fprintf(
			out,
			"\t%s(%s)%s\n",
			query.methodName,
			storeParamsSignature(query.params),
			resultsSignature(query.results),
		)
	}
	out.WriteString("}\n\n")

	fmt.Fprintf(out, "var _ %s = (*%s)(nil)\n", defaultReadInterface, defaultReadType)
	fmt.Fprintf(out, "var _ %s = (*%s)(nil)\n", defaultWriteInterface, defaultWriteType)
	fmt.Fprintf(out, "var _ %s = (*%s)(nil)\n\n", targetName(opts, "Querier"), defaultStoreType)
}

func writeReadQueries(out *bytes.Buffer, opts *options, queries []generatedQuery) {
	fmt.Fprintf(out, "// %s exposes read-only generated queries.\n", defaultReadType)
	fmt.Fprintf(out, "type %s struct {\n", defaultReadType)
	fmt.Fprintf(out, "\tmain *%s\n", targetName(opts, defaultTargetType))
	out.WriteString("}\n\n")

	fmt.Fprintf(
		out,
		"func %s(q *%s) *%s {\n",
		defaultReadNew,
		targetName(opts, defaultTargetType),
		defaultReadType,
	)
	fmt.Fprintf(out, "\treturn &%s{main: q}\n", defaultReadType)
	out.WriteString("}\n\n")

	out.WriteString("// WithTx returns a read wrapper that executes queries through tx.\n")
	fmt.Fprintf(out, "func (%s *%s) WithTx(tx pgx.Tx) *%s {\n", defaultReceiverName, defaultReadType, defaultReadType)
	fmt.Fprintf(out, "\treturn %s(%s.main.WithTx(tx))\n", defaultReadNew, defaultReceiverName)
	out.WriteString("}\n\n")

	writeQueryMethods(out, opts, defaultReadType, queries, queryKindRead, false)
}

func writeWriteQueries(out *bytes.Buffer, opts *options, queries []generatedQuery) {
	fmt.Fprintf(out, "// %s exposes primary-capable generated queries.\n", defaultWriteType)
	fmt.Fprintf(out, "type %s struct {\n", defaultWriteType)
	fmt.Fprintf(out, "\tmain *%s\n", targetName(opts, defaultTargetType))
	fmt.Fprintf(out, "\tmirrors []*%s\n", targetName(opts, defaultTargetType))
	out.WriteString("}\n\n")

	fmt.Fprintf(
		out,
		"func %s(q *%s, mirrors ...*%s) *%s {\n",
		defaultWriteNew,
		targetName(opts, defaultTargetType),
		targetName(opts, defaultTargetType),
		defaultWriteType,
	)
	fmt.Fprintf(out, "\treturn &%s{main: q, mirrors: mirrors}\n", defaultWriteType)
	out.WriteString("}\n\n")

	out.WriteString("// WithTx returns a write wrapper that executes queries through tx.\n")
	fmt.Fprintf(out, "func (%s *%s) WithTx(tx pgx.Tx) *%s {\n", defaultReceiverName, defaultWriteType, defaultWriteType)
	fmt.Fprintf(out, "\treturn %s(%s.main.WithTx(tx))\n", defaultWriteNew, defaultReceiverName)
	out.WriteString("}\n\n")

	out.WriteString("// WithMirrors returns a copy that also writes to the supplied mirrors.\n")
	fmt.Fprintf(
		out,
		"func (%s *%s) WithMirrors(qs ...*%s) *%s {\n",
		defaultReceiverName,
		defaultWriteType,
		defaultWriteType,
		defaultWriteType,
	)
	fmt.Fprintf(out, "\tvar mirrors []*%s\n", targetName(opts, defaultTargetType))
	fmt.Fprintf(out, "\tmirrors = append(mirrors, %s.mirrors...)\n", defaultReceiverName)
	out.WriteString("\tfor _, mirror := range qs {\n")
	out.WriteString("\t\tif mirror == nil {\n")
	out.WriteString("\t\t\tcontinue\n")
	out.WriteString("\t\t}\n")
	out.WriteString("\t\tmirrors = append(mirrors, mirror.main)\n")
	out.WriteString("\t\tmirrors = append(mirrors, mirror.mirrors...)\n")
	out.WriteString("\t}\n")
	fmt.Fprintf(out, "\treturn %s(%s.main, mirrors...)\n", defaultWriteNew, defaultReceiverName)
	out.WriteString("}\n\n")

	fmt.Fprintf(
		out,
		"func (%s *%s) mirror(fn func(*%s) error) error {\n",
		defaultReceiverName,
		defaultWriteType,
		targetName(opts, defaultTargetType),
	)
	fmt.Fprintf(out, "\tfor _, mirror := range %s.mirrors {\n", defaultReceiverName)
	out.WriteString("\t\tif err := fn(mirror); err != nil {\n")
	if opts.IgnoreMirrorError {
		out.WriteString("\t\t\tcontinue\n")
	} else {
		out.WriteString("\t\t\tif errors.Is(err, sql.ErrNoRows) {\n")
		out.WriteString("\t\t\t\tcontinue\n")
		out.WriteString("\t\t\t}\n")
		out.WriteString("\t\t\treturn err\n")
	}
	out.WriteString("\t\t}\n")
	out.WriteString("\t}\n")
	out.WriteString("\treturn nil\n")
	out.WriteString("}\n\n")

	writeQueryMethods(out, opts, defaultWriteType, queries, queryKindWrite, true)
}

func writeQueryStore(out *bytes.Buffer, opts *options) {
	fmt.Fprintf(out, "type %s struct {\n", defaultStoreType)
	fmt.Fprintf(out, "\t*%s\n", defaultReadType)
	fmt.Fprintf(out, "\t*%s\n", defaultWriteType)
	out.WriteString("}\n\n")

	fmt.Fprintf(
		out,
		"func %s(q *%s, mirrors ...*%s) *%s {\n",
		"newQueryStore",
		targetName(opts, defaultTargetType),
		targetName(opts, defaultTargetType),
		defaultStoreType,
	)
	fmt.Fprintf(out, "\treturn &%s{\n", defaultStoreType)
	fmt.Fprintf(out, "\t\t%s:  %s(q),\n", defaultReadType, defaultReadNew)
	fmt.Fprintf(out, "\t\t%s: %s(q, mirrors...),\n", defaultWriteType, defaultWriteNew)
	out.WriteString("\t}\n")
	out.WriteString("}\n\n")

	out.WriteString("// WithTx returns a store wrapper that executes queries through tx.\n")
	fmt.Fprintf(out, "func (%s *%s) WithTx(tx pgx.Tx) *%s {\n", defaultReceiverName, defaultStoreType, defaultStoreType)
	fmt.Fprintf(
		out,
		"\treturn %s(%s.%s.main.WithTx(tx))\n",
		"newQueryStore",
		defaultReceiverName,
		defaultWriteType,
	)
	out.WriteString("}\n\n")

	out.WriteString("// WithMirrors returns a copy that also writes to the supplied mirrors.\n")
	fmt.Fprintf(
		out,
		"func (%s *%s) WithMirrors(qs ...*%s) *%s {\n",
		defaultReceiverName,
		defaultStoreType,
		defaultStoreType,
		defaultStoreType,
	)
	fmt.Fprintf(out, "\tvar mirrors []*%s\n", targetName(opts, defaultTargetType))
	fmt.Fprintf(out, "\tmirrors = append(mirrors, %s.%s.mirrors...)\n", defaultReceiverName, defaultWriteType)
	out.WriteString("\tfor _, mirror := range qs {\n")
	fmt.Fprintf(out, "\t\tif mirror == nil || mirror.%s == nil {\n", defaultWriteType)
	out.WriteString("\t\t\tcontinue\n")
	out.WriteString("\t\t}\n")
	fmt.Fprintf(out, "\t\tmirrors = append(mirrors, mirror.%s.main)\n", defaultWriteType)
	fmt.Fprintf(out, "\t\tmirrors = append(mirrors, mirror.%s.mirrors...)\n", defaultWriteType)
	out.WriteString("\t}\n")
	fmt.Fprintf(
		out,
		"\treturn %s(%s.%s.main, mirrors...)\n",
		"newQueryStore",
		defaultReceiverName,
		defaultWriteType,
	)
	out.WriteString("}\n\n")
}

func writeQueryOptions(out *bytes.Buffer) {
	out.WriteString("type queryOptions struct {\n")
	out.WriteString("\tprimary bool\n")
	out.WriteString("\ttx pgx.Tx\n")
	out.WriteString("}\n\n")
	out.WriteString("// QueryOption customizes routing for one generated query call.\n")
	out.WriteString("type QueryOption func(*queryOptions)\n\n")
	out.WriteString("// ReadFromPrimary routes a read query to the primary database.\n")
	out.WriteString("func ReadFromPrimary() QueryOption {\n")
	out.WriteString("\treturn func(options *queryOptions) { options.primary = true }\n")
	out.WriteString("}\n\n")
	out.WriteString("// WithTx executes a query through tx and suppresses write mirrors.\n")
	out.WriteString("func WithTx(tx pgx.Tx) QueryOption {\n")
	out.WriteString("\treturn func(options *queryOptions) { options.tx = tx }\n")
	out.WriteString("}\n\n")
	out.WriteString("func applyQueryOptions(options ...QueryOption) queryOptions {\n")
	out.WriteString("\tvar result queryOptions\n")
	out.WriteString("\tfor _, option := range options {\n")
	out.WriteString("\t\tif option != nil { option(&result) }\n")
	out.WriteString("\t}\n")
	out.WriteString("\treturn result\n")
	out.WriteString("}\n\n")
}

func writeNodeConstructor(out *bytes.Buffer, opts *options) {
	fmt.Fprintf(
		out,
		"func %s(database %s) pgmesh.Node[*%s, *%s] {\n",
		defaultNodeNew,
		targetName(opts, "DBTX"),
		defaultReadType,
		defaultStoreType,
	)
	fmt.Fprintf(out, "\tqueries := %s(database)\n", targetName(opts, defaultTargetNew))
	fmt.Fprintf(
		out,
		"\treturn pgmesh.NewNode(%s(queries), %s(queries))\n",
		defaultReadNew,
		"newQueryStore",
	)
	out.WriteString("}\n\n")
}

func hasShardRoutes(queries []generatedQuery) bool {
	for index := range queries {
		if queries[index].route != nil {
			return true
		}
	}
	return false
}

func writeStoreConfiguration(out *bytes.Buffer, opts *options, queries []generatedQuery) {
	fmt.Fprintf(out, "// StoreConfig is an opaque database-topology configuration for %s.\n", opts.StoreInterfaceName)
	out.WriteString("type StoreConfig interface {\n")
	fmt.Fprintf(out, "\tbuildStore(context.Context) (%s, error)\n", opts.StoreInterfaceName)
	out.WriteString("}\n\n")

	out.WriteString("// DatabaseConfig configures a single primary, optional read replicas, and optional write mirrors.\n")
	out.WriteString("type DatabaseConfig struct {\n")
	out.WriteString("\t// Name identifies the database in telemetry; empty defaults to \"default\".\n")
	out.WriteString("\tName string\n")
	out.WriteString("\t// Primary serves writes and explicit primary reads. The caller owns its lifecycle.\n")
	fmt.Fprintf(out, "\tPrimary %s\n", targetName(opts, "DBTX"))
	out.WriteString("\t// Replicas serve ordinary reads in round-robin order. The caller owns their lifecycles.\n")
	fmt.Fprintf(out, "\tReplicas []%s\n", targetName(opts, "DBTX"))
	out.WriteString("\t// Mirrors synchronously receive writes in slice order after the primary succeeds.\n")
	fmt.Fprintf(out, "\tMirrors []%s\n", targetName(opts, "DBTX"))
	out.WriteString("\t// TracerProvider records routed query spans; nil uses the global provider.\n")
	out.WriteString("\tTracerProvider trace.TracerProvider\n")
	out.WriteString("\t// MeterProvider records routed query metrics; nil uses the global provider.\n")
	out.WriteString("\tMeterProvider metric.MeterProvider\n")
	out.WriteString("\t// Logger receives routed query debug logs; nil disables logging.\n")
	out.WriteString("\tLogger *slog.Logger\n")
	out.WriteString("}\n\n")

	out.WriteString("type databaseStoreConfig struct { config DatabaseConfig }\n\n")
	out.WriteString("// Database returns an opaque configuration for a non-sharded store.\n")
	out.WriteString("func Database(config DatabaseConfig) StoreConfig {\n")
	out.WriteString("\treturn databaseStoreConfig{config: config}\n")
	out.WriteString("}\n\n")

	fmt.Fprintf(out, "// %s creates the generated query API from an opaque topology configuration.\n", opts.ConstructorName)
	fmt.Fprintf(
		out,
		"func %s(ctx context.Context, config StoreConfig) (%s, error) {\n",
		opts.ConstructorName,
		opts.StoreInterfaceName,
	)
	out.WriteString("\tif config == nil {\n")
	out.WriteString("\t\treturn nil, fmt.Errorf(\"pgmesh: store config is nil\")\n")
	out.WriteString("\t}\n")
	out.WriteString("\treturn config.buildStore(ctx)\n")
	out.WriteString("}\n\n")

	fmt.Fprintf(out, "type %s[SK any] struct {\n", defaultMeshStoreType)
	fmt.Fprintf(out, "\tmesh *pgmesh.Mesh[*%s, *%s, SK]\n", defaultReadType, defaultStoreType)
	if hasShardRoutes(queries) {
		fmt.Fprintf(out, "\tresolver %s[SK]\n", opts.ResolverInterfaceName)
	}
	out.WriteString("}\n\n")
	fmt.Fprintf(out, "var _ %s = (*%s[uint8])(nil)\n\n", opts.StoreInterfaceName, defaultMeshStoreType)

	fmt.Fprintf(
		out,
		"func (c databaseStoreConfig) buildStore(_ context.Context) (%s, error) {\n",
		opts.StoreInterfaceName,
	)
	out.WriteString("\tconfig := c.config\n")
	out.WriteString("\tif config.Name == \"\" { config.Name = \"default\" }\n")
	out.WriteString("\tif config.Primary == nil {\n")
	out.WriteString("\t\treturn nil, fmt.Errorf(\"pgmesh: database primary is nil\")\n")
	out.WriteString("\t}\n")
	out.WriteString("\tfor index, database := range config.Replicas {\n")
	out.WriteString("\t\tif database == nil { return nil, fmt.Errorf(\"pgmesh: database replica %d is nil\", index) }\n")
	out.WriteString("\t}\n")
	out.WriteString("\tfor index, database := range config.Mirrors {\n")
	out.WriteString("\t\tif database == nil { return nil, fmt.Errorf(\"pgmesh: database mirror %d is nil\", index) }\n")
	out.WriteString("\t}\n")
	fmt.Fprintf(out, "\tprimary := %s(config.Primary)\n", defaultNodeNew)
	fmt.Fprintf(out, "\treplicas := make([]pgmesh.Node[*%s, *%s], 0, len(config.Replicas))\n", defaultReadType, defaultStoreType)
	out.WriteString("\tfor _, database := range config.Replicas {\n")
	fmt.Fprintf(out, "\t\treplicas = append(replicas, %s(database))\n", defaultNodeNew)
	out.WriteString("\t}\n")
	out.WriteString("\treplicaSet := pgmesh.NewReplicaSet(config.Name, primary, replicas)\n")
	fmt.Fprintf(out, "\tmirrors := make([]*%s, 0, len(config.Mirrors))\n", defaultStoreType)
	out.WriteString("\tfor _, database := range config.Mirrors {\n")
	fmt.Fprintf(out, "\t\tmirrors = append(mirrors, %s(database).Writer())\n", defaultNodeNew)
	out.WriteString("\t}\n")
	out.WriteString("\treplicaSet = replicaSet.WithWriteMirrors(mirrors...)\n")
	fmt.Fprintf(out, "\tmesh, err := pgmesh.NewBuilder[*%s, *%s, uint8](1).\n", defaultReadType, defaultStoreType)
	out.WriteString("\t\tWithHasher(pgmesh.ConstantShardHashFor[uint8](0)).\n")
	out.WriteString("\t\tWithTracerProvider(config.TracerProvider).\n")
	out.WriteString("\t\tWithMeterProvider(config.MeterProvider).\n")
	out.WriteString("\t\tWithLogger(config.Logger).\n")
	out.WriteString("\t\tLink(0, replicaSet).\n")
	out.WriteString("\t\tBuild()\n")
	out.WriteString("\tif err != nil { return nil, err }\n")
	fmt.Fprintf(
		out,
		"\treturn &%s[uint8]{mesh: mesh}, nil\n",
		defaultMeshStoreType,
	)
	out.WriteString("}\n\n")

	for index := range queries {
		writeMeshStoreQueryMethod(out, opts, &queries[index])
	}
}

func writeMeshStoreQueryMethod(out *bytes.Buffer, opts *options, query *generatedQuery) {
	traced := lastResultIsError(query.results)
	resultSignature := resultsSignature(query.results)
	var resultNames []string
	var errName string
	if traced {
		resultSignature, resultNames, errName = namedResultsSignature(
			query.params,
			query.results,
			defaultReceiverName,
			"storeOptions",
		)
	}
	fmt.Fprintf(out, "// %s executes the generated query on its target shard.\n", query.methodName)
	fmt.Fprintf(
		out,
		"func (%s *%s[SK]) %s(%s)%s {\n",
		defaultReceiverName,
		defaultMeshStoreType,
		query.methodName,
		storeParamsSignature(query.params),
		resultSignature,
	)
	if traced {
		out.WriteString("\t// Trace the query and record its returned error.\n")
		fmt.Fprintf(
			out,
			"\tctx, querySpan := %s.mesh.StartSpan(ctx, %q, %q, %s)\n",
			defaultReceiverName,
			opts.StoreInterfaceName,
			query.methodName,
			queryKindConstant(query.kind),
		)
		fmt.Fprintf(out, "\tdefer func() { querySpan.End(%s) }()\n\n", errName)
	}

	out.WriteString("\t// Resolve the shard key for this topology.\n")
	out.WriteString("\tvar shardKey SK\n")
	if query.route != nil {
		fmt.Fprintf(out, "\tif %s.resolver != nil {\n", defaultReceiverName)
		routeArgs := make([]string, 0, len(query.route.operands))
		for _, operand := range query.route.operands {
			routeArgs = append(routeArgs, operand.expression)
		}
		fmt.Fprintf(
			out,
			"\t\tshardKey = %s.resolver.%s(%s)\n",
			defaultReceiverName,
			query.route.methodName,
			strings.Join(routeArgs, ", "),
		)
		out.WriteString("\t}\n")
	}
	if traced {
		fmt.Fprintf(out, "\tshard, %s := %s.mesh.Shard(shardKey)\n", errName, defaultReceiverName)
		fmt.Fprintf(out, "\tif %s != nil {\n", errName)
		fmt.Fprintf(out, "\t\treturn %s\n", strings.Join(resultNames, ", "))
		out.WriteString("\t}\n")
	} else {
		fmt.Fprintf(out, "\tshard, _ := %s.mesh.Shard(shardKey)\n", defaultReceiverName)
	}

	out.WriteString("\n\t// Apply options that can override the default route.\n")
	out.WriteString("\toptions := applyQueryOptions(storeOptions...)\n")
	args := callArguments(query.params)
	if query.kind == queryKindRead {
		out.WriteString("\n\t// Transactional reads must use their transaction.\n")
		out.WriteString("\tif options.tx != nil {\n")
		if traced {
			out.WriteString("\t\tquerySpan.SetRoute(shard.VShardIndex(), shard.Name(), pgmesh.RouteModeTransaction, 0)\n")
		}
		fmt.Fprintf(
			out,
			"\t\treturn shard.Write().WithTx(options.tx).%s(%s)\n",
			query.methodName,
			args,
		)
		out.WriteString("\t}\n")

		out.WriteString("\n\t// Explicit primary reads bypass replicas.\n")
		out.WriteString("\tif options.primary {\n")
		if traced {
			out.WriteString("\t\tquerySpan.SetRoute(shard.VShardIndex(), shard.Name(), pgmesh.RouteModePrimary, 0)\n")
		}
		fmt.Fprintf(out, "\t\treturn shard.Write().%s(%s)\n", query.methodName, args)
		out.WriteString("\t}\n")

		out.WriteString("\n\t// Ordinary reads use the shard's replica route.\n")
		if traced {
			out.WriteString("\tquerySpan.SetRoute(shard.VShardIndex(), shard.Name(), pgmesh.RouteModeRead, 0)\n")
		}
		fmt.Fprintf(out, "\treturn shard.Read().%s(%s)\n", query.methodName, args)
	} else {
		out.WriteString("\n\t// Select the primary write route, or the transaction when provided.\n")
		out.WriteString("\ttarget := shard.Write()\n")
		if traced {
			out.WriteString("\tmode := pgmesh.RouteModePrimary\n")
			out.WriteString("\twriteMirrorCount := shard.WriteMirrorCount()\n")
		}
		out.WriteString("\tif options.tx != nil {\n")
		out.WriteString("\t\ttarget = target.WithTx(options.tx)\n")
		if traced {
			out.WriteString("\t\tmode = pgmesh.RouteModeTransaction\n")
			out.WriteString("\t\twriteMirrorCount = 0\n")
		}
		out.WriteString("\t}\n")

		if traced {
			out.WriteString("\n\t// Execute the write after recording its resolved route.\n")
			out.WriteString("\tquerySpan.SetRoute(shard.VShardIndex(), shard.Name(), mode, writeMirrorCount)\n")
		} else {
			out.WriteString("\n\t// Execute the write on the selected target.\n")
		}
		fmt.Fprintf(out, "\treturn target.%s(%s)\n", query.methodName, args)
	}
	out.WriteString("}\n\n")
}

func writeShardedStore(
	out *bytes.Buffer,
	opts *options,
) {
	out.WriteString("// ShardDatabaseConfig configures one primary and its read replicas.\n")
	out.WriteString("type ShardDatabaseConfig struct {\n")
	out.WriteString("\t// Name uniquely identifies the physical replica set.\n")
	out.WriteString("\tName string\n")
	out.WriteString("\t// Primary serves writes and primary reads. The caller owns its lifecycle.\n")
	fmt.Fprintf(out, "\tPrimary %s\n", targetName(opts, "DBTX"))
	out.WriteString("\t// Replicas serve ordinary reads in round-robin order. The caller owns their lifecycles.\n")
	fmt.Fprintf(out, "\tReplicas []%s\n", targetName(opts, "DBTX"))
	out.WriteString("}\n\n")

	out.WriteString("// ShardedConfig configures shard routing behind the generated Store API.\n")
	out.WriteString("type ShardedConfig[SK any] struct {\n")
	out.WriteString("\t// ReplicaSets define the physical database nodes in the topology.\n")
	out.WriteString("\tReplicaSets []ShardDatabaseConfig\n")
	out.WriteString("\t// Shards maps every virtual shard to a primary replica set and ordered mirrors.\n")
	out.WriteString("\tShards pgmesh.Shards\n")
	out.WriteString("\t// ShardHasher maps resolved shard keys to virtual shard indexes.\n")
	out.WriteString("\tShardHasher pgmesh.ShardHasher[SK]\n")
	out.WriteString("\t// Resolver extracts application shard keys from generated query parameters.\n")
	fmt.Fprintf(out, "\tResolver %s[SK]\n", opts.ResolverInterfaceName)
	out.WriteString("\t// TracerProvider records routed query spans; nil uses the global provider.\n")
	out.WriteString("\tTracerProvider trace.TracerProvider\n")
	out.WriteString("\t// MeterProvider records routed query metrics; nil uses the global provider.\n")
	out.WriteString("\tMeterProvider metric.MeterProvider\n")
	out.WriteString("\t// Logger receives routed query debug logs; nil disables logging.\n")
	out.WriteString("\tLogger *slog.Logger\n")
	out.WriteString("}\n\n")

	fmt.Fprintf(out, "type shardedStoreConfig[SK any] struct { config ShardedConfig[SK] }\n\n")
	fmt.Fprintf(out, "// %s returns an opaque sharded topology configuration.\n", opts.ShardedConstructor)
	fmt.Fprintf(out, "func %s[SK any](config ShardedConfig[SK]) StoreConfig {\n", opts.ShardedConstructor)
	out.WriteString("\treturn shardedStoreConfig[SK]{config: config}\n")
	out.WriteString("}\n\n")

	fmt.Fprintf(
		out,
		"func (c shardedStoreConfig[SK]) buildStore(ctx context.Context) (%s, error) {\n",
		opts.StoreInterfaceName,
	)
	out.WriteString("\tconfig := c.config\n")
	out.WriteString("\tif config.Resolver == nil {\n")
	out.WriteString("\t\treturn nil, fmt.Errorf(\"pgmesh: shard resolver is nil\")\n")
	out.WriteString("\t}\n")
	fmt.Fprintf(out, "\tnodes := make(map[string]%s)\n", targetName(opts, "DBTX"))
	out.WriteString("\tspecs := make([]pgmesh.ReplicaSetSpec, 0, len(config.ReplicaSets))\n")
	out.WriteString("\tfor setIndex, set := range config.ReplicaSets {\n")
	out.WriteString("\t\tprimaryDSN := fmt.Sprintf(\"pgmesh-internal://%d/primary\", setIndex)\n")
	out.WriteString("\t\tnodes[primaryDSN] = set.Primary\n")
	out.WriteString("\t\treplicas := make([]pgmesh.Connection, 0, len(set.Replicas))\n")
	out.WriteString("\t\tfor replicaIndex, database := range set.Replicas {\n")
	out.WriteString("\t\t\tdsn := fmt.Sprintf(\"pgmesh-internal://%d/replica/%d\", setIndex, replicaIndex)\n")
	out.WriteString("\t\t\tnodes[dsn] = database\n")
	out.WriteString("\t\t\treplicas = append(replicas, pgmesh.Connection{DSN: dsn})\n")
	out.WriteString("\t\t}\n")
	out.WriteString("\t\tspecs = append(specs, pgmesh.ReplicaSetSpec{\n")
	out.WriteString("\t\t\tName: set.Name,\n")
	out.WriteString("\t\t\tPrimary: pgmesh.Connection{DSN: primaryDSN},\n")
	out.WriteString("\t\t\tReplicas: replicas,\n")
	out.WriteString("\t\t})\n")
	out.WriteString("\t}\n")
	fmt.Fprintf(
		out,
		"\tmesh, err := pgmesh.CreateMesh(ctx, &pgmesh.Options[*%s, *%s, SK]{\n",
		defaultReadType,
		defaultStoreType,
	)
	out.WriteString("\t\tReplicaSets: specs,\n")
	out.WriteString("\t\tShards: config.Shards,\n")
	fmt.Fprintf(
		out,
		"\t\tCreateNode: func(_ context.Context, dsn string) (pgmesh.Node[*%s, *%s], error) {\n",
		defaultReadType,
		defaultStoreType,
	)
	out.WriteString("\t\t\tdatabase, ok := nodes[dsn]\n")
	out.WriteString("\t\t\tif !ok || database == nil {\n")
	fmt.Fprintf(
		out,
		"\t\t\t\treturn pgmesh.Node[*%s, *%s]{}, fmt.Errorf(\"pgmesh: database node %%q is nil\", dsn)\n",
		defaultReadType,
		defaultStoreType,
	)
	out.WriteString("\t\t\t}\n")
	fmt.Fprintf(out, "\t\t\treturn %s(database), nil\n", defaultNodeNew)
	out.WriteString("\t\t},\n")
	out.WriteString("\t\tShardHasher: config.ShardHasher,\n")
	out.WriteString("\t\tTracerProvider: config.TracerProvider,\n")
	out.WriteString("\t\tMeterProvider: config.MeterProvider,\n")
	out.WriteString("\t\tLogger: config.Logger,\n")
	out.WriteString("\t})\n")
	out.WriteString("\tif err != nil { return nil, err }\n")
	fmt.Fprintf(
		out,
		"\treturn &%s[SK]{mesh: mesh, resolver: config.Resolver}, nil\n",
		defaultMeshStoreType,
	)
	out.WriteString("}\n\n")
}

func writeShardResolverInterface(out *bytes.Buffer, opts *options, routes []shardRoute) {
	fmt.Fprintf(out, "// %s resolves generated query parameters to shard keys.\n", opts.ResolverInterfaceName)
	fmt.Fprintf(out, "type %s[SK any] interface {\n", opts.ResolverInterfaceName)
	for _, route := range routes {
		fmt.Fprintf(out, "\t// %s resolves the %q shard route.\n", route.methodName, route.name)
		fmt.Fprintf(out, "\t%s(%s) SK\n", route.methodName, routeOperandsSignature(route.operands))
	}
	out.WriteString("}\n\n")
}

func routeOperandsSignature(operands []routeOperand) string {
	parts := make([]string, 0, len(operands))
	for _, operand := range operands {
		parts = append(parts, operand.name+" "+operand.typ)
	}
	return strings.Join(parts, ", ")
}

func writeQueryMethods(
	out *bytes.Buffer,
	opts *options,
	receiverType string,
	queries []generatedQuery,
	kind queryKind,
	mirror bool,
) {
	for idx := range queries {
		query := &queries[idx]
		if query.kind != kind {
			continue
		}
		fmt.Fprintf(out, "// %s executes the generated %s query.\n", query.methodName, query.methodName)
		fmt.Fprintf(out, "func (%s *%s) %s(%s)%s {\n",
			defaultReceiverName,
			receiverType,
			query.methodName,
			paramsSignature(query.params),
			resultsSignature(query.results),
		)
		writeQueryMethodBody(out, opts, query, mirror)
		out.WriteString("}\n\n")
	}
}

func writeQueryMethodBody(out *bytes.Buffer, opts *options, query *generatedQuery, mirror bool) {
	args := callArguments(query.params)
	if !lastResultIsError(query.results) {
		fmt.Fprintf(out, "\treturn %s.main.%s(%s)\n", defaultReceiverName, query.methodName, args)
		return
	}

	nonErrorResults := query.results[:len(query.results)-1]
	if len(nonErrorResults) == 0 {
		fmt.Fprintf(out, "\tif err := %s.main.%s(%s); err != nil {\n", defaultReceiverName, query.methodName, args)
		out.WriteString("\t\treturn err\n")
		out.WriteString("\t}\n")
		if !mirror {
			out.WriteString("\treturn nil\n")
			return
		}
		if opts.IgnoreMirrorError {
			fmt.Fprintf(
				out,
				"\t_ = %s.mirror(func(%s *%s) error {\n",
				defaultReceiverName,
				mirrorReceiverName,
				targetName(opts, defaultTargetType),
			)
			fmt.Fprintf(out, "\t\treturn %s.%s(%s)\n", mirrorReceiverName, query.methodName, args)
			out.WriteString("\t})\n")
			out.WriteString("\treturn nil\n")
			return
		}
		fmt.Fprintf(
			out,
			"\treturn %s.mirror(func(%s *%s) error {\n",
			defaultReceiverName,
			mirrorReceiverName,
			targetName(opts, defaultTargetType),
		)
		fmt.Fprintf(out, "\t\treturn %s.%s(%s)\n", mirrorReceiverName, query.methodName, args)
		out.WriteString("\t})\n")
		return
	}

	resultVars := resultVariables(len(nonErrorResults))
	fmt.Fprintf(out, "\t%s := %s.main.%s(%s)\n",
		strings.Join(appendResultError(resultVars), ", "),
		defaultReceiverName,
		query.methodName,
		args,
	)
	out.WriteString("\tif err != nil {\n")
	for idx, result := range nonErrorResults {
		fmt.Fprintf(out, "\t\tvar zero%d %s\n", idx, result)
	}
	out.WriteString("\t\treturn ")
	for idx := range nonErrorResults {
		if idx > 0 {
			out.WriteString(", ")
		}
		fmt.Fprintf(out, "zero%d", idx)
	}
	out.WriteString(", err\n")
	out.WriteString("\t}\n")

	if !mirror {
		fmt.Fprintf(out, "\treturn %s, nil\n", strings.Join(resultVars, ", "))
		return
	}
	if opts.IgnoreMirrorError {
		fmt.Fprintf(
			out,
			"\t_ = %s.mirror(func(%s *%s) error {\n",
			defaultReceiverName,
			mirrorReceiverName,
			targetName(opts, defaultTargetType),
		)
		fmt.Fprintf(out, "\t\t%s := %s.%s(%s)\n",
			strings.Join(discardVariables(len(nonErrorResults)+1), ", "),
			mirrorReceiverName,
			query.methodName,
			args,
		)
		out.WriteString("\t\treturn err\n")
		out.WriteString("\t})\n")
		fmt.Fprintf(out, "\treturn %s, nil\n", strings.Join(resultVars, ", "))
		return
	}

	fmt.Fprintf(
		out,
		"\t%s := %s.mirror(func(%s *%s) error {\n",
		mirrorErrorName,
		defaultReceiverName,
		mirrorReceiverName,
		targetName(opts, defaultTargetType),
	)
	fmt.Fprintf(out, "\t\t%s := %s.%s(%s)\n",
		strings.Join(discardVariables(len(nonErrorResults)+1), ", "),
		mirrorReceiverName,
		query.methodName,
		args,
	)
	out.WriteString("\t\treturn err\n")
	out.WriteString("\t})\n")
	fmt.Fprintf(out, "\treturn %s, %s\n", strings.Join(resultVars, ", "), mirrorErrorName)
}

func callArguments(params []argument) string {
	args := make([]string, 0, len(params))
	for _, param := range params {
		args = append(args, param.name)
	}
	return strings.Join(args, ", ")
}

func lastResultIsError(results []string) bool {
	return len(results) > 0 && results[len(results)-1] == resultErrorName
}

func resultVariables(count int) []string {
	vars := make([]string, count)
	for idx := range vars {
		vars[idx] = fmt.Sprintf("rv%d", idx)
	}
	return vars
}

func appendResultError(vars []string) []string {
	out := make([]string, 0, len(vars)+1)
	out = append(out, vars...)
	out = append(out, "err")
	return out
}

func discardVariables(count int) []string {
	vars := make([]string, count)
	for idx := range count - 1 {
		vars[idx] = "_"
	}
	vars[count-1] = "err"
	return vars
}

func writeSplitInterface(out *bytes.Buffer, name string, queries []generatedQuery, kind queryKind) {
	fmt.Fprintf(out, "// %s exposes generated %s queries.\n", name, kind)
	fmt.Fprintf(out, "type %s interface {\n", name)
	for _, query := range queries {
		if query.kind != kind {
			continue
		}
		fmt.Fprintf(out, "\t// %s executes the generated %s query.\n", query.methodName, query.methodName)
		fmt.Fprintf(out, "\t%s(%s)%s\n", query.methodName, paramsSignature(query.params), resultsSignature(query.results))
	}
	out.WriteString("}\n\n")
}

func targetName(opts *options, name string) string {
	if opts.InternalImportPath == "" {
		return name
	}
	return internalQualifier(opts) + "." + name
}

func internalQualifier(opts *options) string {
	if opts.InternalImportAlias != "" {
		return opts.InternalImportAlias
	}
	return "internal"
}

func importAlias(alias string) string {
	if alias == "" || alias == "internal" {
		return ""
	}
	return alias
}

func writeImports(out *bytes.Buffer, imports []importSpec) {
	if len(imports) == 0 {
		return
	}
	out.WriteString("import (\n")
	for _, imp := range imports {
		if imp.name != "" {
			fmt.Fprintf(out, "\t%s %q\n", imp.name, imp.path)
			continue
		}
		fmt.Fprintf(out, "\t%q\n", imp.path)
	}
	out.WriteString(")\n\n")
}
