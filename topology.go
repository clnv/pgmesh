package pgmesh

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// NodeFactory opens the database node identified by a DSN. Nodes and their
// underlying pools remain caller-owned; pgmesh does not close them.
type NodeFactory[R any, W Mirrorable[W]] func(context.Context, string) (Node[R, W], error)

type connection struct {
	dsn string
}

type replicaSetSpec struct {
	name     string
	primary  connection
	replicas []connection
}

type vshardMapping struct {
	vshards           []uint64
	mainReplicaSet    string
	mirrorReplicaSets []string
}

type meshConfig struct {
	replicaSets    []replicaSetSpec
	numVShards     uint64
	mappings       []vshardMapping
	tracerProvider trace.TracerProvider
	meterProvider  metric.MeterProvider
	logger         *slog.Logger
}

// MeshOption customizes a declaratively constructed mesh.
type MeshOption func(*meshConfig)

// WithReplicaSet registers a named primary and its optional read replicas.
// Repeated calls append replica sets in call order.
func WithReplicaSet(name, primaryDSN string, replicaDSNs ...string) MeshOption {
	replicas := append([]string(nil), replicaDSNs...)
	return func(config *meshConfig) {
		spec := replicaSetSpec{
			name:     name,
			primary:  connection{dsn: primaryDSN},
			replicas: make([]connection, 0, len(replicas)),
		}
		for _, dsn := range replicas {
			spec.replicas = append(spec.replicas, connection{dsn: dsn})
		}
		config.replicaSets = append(config.replicaSets, spec)
	}
}

// WithVShardMapping maps virtual shards to a main replica set and optional
// ordered write mirrors. Repeated calls append mappings in call order.
func WithVShardMapping(
	mainReplicaSet string,
	vshards []uint64,
	mirrorReplicaSets ...string,
) MeshOption {
	shards := append([]uint64(nil), vshards...)
	mirrors := append([]string(nil), mirrorReplicaSets...)
	return func(config *meshConfig) {
		config.mappings = append(config.mappings, vshardMapping{
			vshards:           append([]uint64(nil), shards...),
			mainReplicaSet:    mainReplicaSet,
			mirrorReplicaSets: append([]string(nil), mirrors...),
		})
	}
}

// WithTracerProvider configures the provider used for routed query spans.
// A nil provider uses the global OpenTelemetry tracer provider.
func WithTracerProvider(provider trace.TracerProvider) MeshOption {
	return func(config *meshConfig) {
		config.tracerProvider = provider
	}
}

// WithMeterProvider configures the provider used for routed query metrics.
// A nil provider uses the global OpenTelemetry meter provider.
func WithMeterProvider(provider metric.MeterProvider) MeshOption {
	return func(config *meshConfig) {
		config.meterProvider = provider
	}
}

// WithLogger configures optional structured logging for routed queries.
// A nil logger disables logging.
func WithLogger(logger *slog.Logger) MeshOption {
	return func(config *meshConfig) {
		config.logger = logger
	}
}

// VShardRange returns the half-open virtual shard range [from, to).
func VShardRange(from, to uint64) []uint64 {
	if to <= from {
		return []uint64{}
	}
	out := make([]uint64, 0, to-from)
	for index := from; index < to; index++ {
		out = append(out, index)
	}
	return out
}

// CreateMesh validates its configuration, opens its database nodes, and builds
// an immutable mesh. It calls createNode once for each primary and replica, in
// option order, and stops at the first error. Successfully created nodes are
// not closed on a later error and remain caller-owned.
func CreateMesh[R any, W Mirrorable[W], SK any](
	ctx context.Context,
	numVShards uint64,
	createNode NodeFactory[R, W],
	shardHasher ShardHasher[SK],
	options ...MeshOption,
) (*Mesh[R, W, SK], error) {
	config := meshConfig{
		replicaSets:    nil,
		numVShards:     numVShards,
		mappings:       nil,
		tracerProvider: nil,
		meterProvider:  nil,
		logger:         nil,
	}
	for index, option := range options {
		if option == nil {
			return nil, fmt.Errorf("pgmesh: mesh option %d is nil", index)
		}
		option(&config)
	}
	if err := validateMeshConfig(&config, createNode, shardHasher); err != nil {
		return nil, err
	}

	replicaSets := make(map[string]*ReplicaSet[R, W], len(config.replicaSets))
	for _, spec := range config.replicaSets {
		primary, err := createNode(ctx, spec.primary.dsn)
		if err != nil {
			return nil, fmt.Errorf("create primary node for replica set %q: %w", spec.name, err)
		}
		replicas := make([]Node[R, W], 0, len(spec.replicas))
		for _, connection := range spec.replicas {
			replica, err := createNode(ctx, connection.dsn)
			if err != nil {
				return nil, fmt.Errorf("create replica node for replica set %q: %w", spec.name, err)
			}
			replicas = append(replicas, replica)
		}
		replicaSets[spec.name] = NewReplicaSet(spec.name, primary, replicas)
	}

	configured := make(map[string]*ReplicaSet[R, W], len(replicaSets))
	for _, mapping := range config.mappings {
		if _, ok := configured[mapping.mainReplicaSet]; ok {
			continue
		}
		main := replicaSets[mapping.mainReplicaSet]
		mirrors := make([]W, 0, len(mapping.mirrorReplicaSets))
		for _, name := range mapping.mirrorReplicaSets {
			mirrors = append(mirrors, replicaSets[name].primaryWriter())
		}
		configured[mapping.mainReplicaSet] = main.WithWriteMirrors(mirrors...)
	}

	builder := NewBuilder[R, W, SK](config.numVShards).
		WithHasher(shardHasher).
		WithTracerProvider(config.tracerProvider).
		WithMeterProvider(config.meterProvider).
		WithLogger(config.logger)
	for _, mapping := range config.mappings {
		for _, vshard := range mapping.vshards {
			builder.Link(vshard, configured[mapping.mainReplicaSet])
		}
	}
	return builder.Build()
}

func validateMeshConfig[R any, W Mirrorable[W], SK any](
	config *meshConfig,
	createNode NodeFactory[R, W],
	shardHasher ShardHasher[SK],
) error {
	if len(config.replicaSets) == 0 {
		return ErrNoReplicaSets
	}
	if createNode == nil {
		return ErrNoNodeFactory
	}
	if shardHasher == nil {
		return ErrNoShardHasher
	}
	if config.numVShards == 0 {
		return ErrNoVShards
	}

	names := make(map[string]struct{}, len(config.replicaSets))
	for _, spec := range config.replicaSets {
		if strings.TrimSpace(spec.name) == "" {
			return ErrEmptyReplicaSetName
		}
		if _, ok := names[spec.name]; ok {
			return fmt.Errorf("%w: %q", ErrDuplicateReplicaSet, spec.name)
		}
		names[spec.name] = struct{}{}
		if strings.TrimSpace(spec.primary.dsn) == "" {
			return fmt.Errorf("%w: primary of %q", ErrEmptyDSN, spec.name)
		}
		for index, replica := range spec.replicas {
			if strings.TrimSpace(replica.dsn) == "" {
				return fmt.Errorf("%w: replica %d of %q", ErrEmptyDSN, index, spec.name)
			}
		}
	}

	linked := make([]bool, config.numVShards)
	mirrorConfigurations := make(map[string]string)
	for _, mapping := range config.mappings {
		if _, ok := names[mapping.mainReplicaSet]; !ok {
			return fmt.Errorf("%w: main %q", ErrUnknownReplicaSet, mapping.mainReplicaSet)
		}
		seenMirrors := make(map[string]struct{}, len(mapping.mirrorReplicaSets))
		for _, mirror := range mapping.mirrorReplicaSets {
			if _, ok := names[mirror]; !ok {
				return fmt.Errorf("%w: mirror %q", ErrUnknownReplicaSet, mirror)
			}
			if mirror == mapping.mainReplicaSet {
				return fmt.Errorf("%w: replica set %q cannot mirror itself", ErrMirrorConfiguration, mirror)
			}
			if _, ok := seenMirrors[mirror]; ok {
				return fmt.Errorf("%w: duplicate mirror %q", ErrMirrorConfiguration, mirror)
			}
			seenMirrors[mirror] = struct{}{}
		}
		configuration := strings.Join(mapping.mirrorReplicaSets, "\x00")
		if previous, ok := mirrorConfigurations[mapping.mainReplicaSet]; ok && previous != configuration {
			return fmt.Errorf("%w for %q", ErrMirrorConfiguration, mapping.mainReplicaSet)
		}
		mirrorConfigurations[mapping.mainReplicaSet] = configuration

		for _, vshard := range mapping.vshards {
			if vshard >= config.numVShards {
				return fmt.Errorf("%w: %d", ErrVShardOutOfRange, vshard)
			}
			if linked[vshard] {
				return fmt.Errorf("%w: %d", ErrDuplicateVShard, vshard)
			}
			linked[vshard] = true
		}
	}
	for index, ok := range linked {
		if !ok {
			return fmt.Errorf("%w: %d", ErrMissingVShard, index)
		}
	}
	return nil
}
