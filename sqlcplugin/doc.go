// Package sqlcplugin generates pgmesh query wrappers from sqlc metadata.
//
// Generate is the package entry point. It accepts PostgreSQL metadata and
// pgx/v5-compatible options, validates shard annotations and configuration,
// and returns the generated Store file set. Plugin options are intentionally
// internal and decoded strictly so unsupported or misspelled fields fail
// generation.
package sqlcplugin
