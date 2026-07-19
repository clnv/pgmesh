package pgmesh

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Connection identifies a database node by its connection string.
type Connection struct {
	// DSN is the PostgreSQL data source name passed to Options.CreateNode.
	DSN string
}

// ReplicaSetSpec describes a primary database and its read replicas.
type ReplicaSetSpec struct {
	// Name uniquely identifies the replica set within a topology.
	Name string
	// Primary is the replica set's writable database node.
	Primary Connection
	// Replicas are read-only nodes used for round-robin reads.
	Replicas []Connection
}

// Shards describes the virtual-shard topology and its physical mappings.
type Shards struct {
	// NumVShards is the total number of virtual shards in the topology.
	NumVShards uint64
	// Mappings assign every virtual shard to a physical replica set.
	Mappings []VShardMapping
}

// VShardMapping assigns virtual shards to a main replica set and write mirrors.
type VShardMapping struct {
	// VShards are the virtual shard indexes covered by this mapping.
	VShards []uint64
	// MainReplicaSet names the replica set that serves reads and primary writes.
	MainReplicaSet string
	// MirrorReplicaSets name replica sets that synchronously receive writes.
	MirrorReplicaSets []string
}

// Options configures declarative mesh construction.
type Options[R any, W Mirrorable[W], SK any] struct {
	// ReplicaSets define the physical database nodes in the topology.
	ReplicaSets []ReplicaSetSpec
	// Shards defines virtual shard placement and write mirrors.
	Shards Shards

	// CreateNode opens the node identified by a DSN.
	CreateNode func(context.Context, string) (Node[R, W], error)
	// ShardHasher maps application shard keys to virtual shard indexes.
	ShardHasher ShardHasher[SK]
	// TracerProvider records routed query spans; nil uses the global provider.
	TracerProvider trace.TracerProvider
	// MeterProvider records routed query metrics; nil uses the global provider.
	MeterProvider metric.MeterProvider
	// Logger receives routed query debug logs; nil disables logging.
	Logger *slog.Logger
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

// CreateMesh validates opts, opens its database nodes, and builds an immutable mesh.
func CreateMesh[R any, W Mirrorable[W], SK any](
	ctx context.Context,
	opts *Options[R, W, SK],
) (*Mesh[R, W, SK], error) {
	if err := validateOptions(opts); err != nil {
		return nil, err
	}

	replicaSets := make(map[string]*ReplicaSet[R, W], len(opts.ReplicaSets))
	for _, spec := range opts.ReplicaSets {
		primary, err := opts.CreateNode(ctx, spec.Primary.DSN)
		if err != nil {
			return nil, fmt.Errorf("create primary node for replica set %q: %w", spec.Name, err)
		}
		replicas := make([]Node[R, W], 0, len(spec.Replicas))
		for _, connection := range spec.Replicas {
			replica, err := opts.CreateNode(ctx, connection.DSN)
			if err != nil {
				return nil, fmt.Errorf("create replica node for replica set %q: %w", spec.Name, err)
			}
			replicas = append(replicas, replica)
		}
		replicaSets[spec.Name] = NewReplicaSet(spec.Name, primary, replicas)
	}

	configured := make(map[string]*ReplicaSet[R, W], len(replicaSets))
	for _, mapping := range opts.Shards.Mappings {
		if _, ok := configured[mapping.MainReplicaSet]; ok {
			continue
		}
		main := replicaSets[mapping.MainReplicaSet]
		mirrors := make([]W, 0, len(mapping.MirrorReplicaSets))
		for _, name := range mapping.MirrorReplicaSets {
			mirrors = append(mirrors, replicaSets[name].primaryWriter())
		}
		configured[mapping.MainReplicaSet] = main.WithWriteMirrors(mirrors...)
	}

	builder := NewBuilder[R, W, SK](opts.Shards.NumVShards).
		WithHasher(opts.ShardHasher).
		WithTracerProvider(opts.TracerProvider).
		WithMeterProvider(opts.MeterProvider).
		WithLogger(opts.Logger)
	for _, mapping := range opts.Shards.Mappings {
		for _, vshard := range mapping.VShards {
			builder.Link(vshard, configured[mapping.MainReplicaSet])
		}
	}
	return builder.Build()
}

func validateOptions[R any, W Mirrorable[W], SK any](opts *Options[R, W, SK]) error {
	if opts == nil || len(opts.ReplicaSets) == 0 {
		return ErrNoReplicaSets
	}
	if opts.CreateNode == nil {
		return ErrNoNodeFactory
	}
	if opts.ShardHasher == nil {
		return ErrNoShardHasher
	}
	if opts.Shards.NumVShards == 0 {
		return ErrNoVShards
	}

	names := make(map[string]struct{}, len(opts.ReplicaSets))
	for _, spec := range opts.ReplicaSets {
		if strings.TrimSpace(spec.Name) == "" {
			return ErrEmptyReplicaSetName
		}
		if _, ok := names[spec.Name]; ok {
			return fmt.Errorf("%w: %q", ErrDuplicateReplicaSet, spec.Name)
		}
		names[spec.Name] = struct{}{}
		if strings.TrimSpace(spec.Primary.DSN) == "" {
			return fmt.Errorf("%w: primary of %q", ErrEmptyDSN, spec.Name)
		}
		for index, replica := range spec.Replicas {
			if strings.TrimSpace(replica.DSN) == "" {
				return fmt.Errorf("%w: replica %d of %q", ErrEmptyDSN, index, spec.Name)
			}
		}
	}

	linked := make([]bool, opts.Shards.NumVShards)
	mirrorConfigurations := make(map[string]string)
	for _, mapping := range opts.Shards.Mappings {
		if _, ok := names[mapping.MainReplicaSet]; !ok {
			return fmt.Errorf("%w: main %q", ErrUnknownReplicaSet, mapping.MainReplicaSet)
		}
		seenMirrors := make(map[string]struct{}, len(mapping.MirrorReplicaSets))
		for _, mirror := range mapping.MirrorReplicaSets {
			if _, ok := names[mirror]; !ok {
				return fmt.Errorf("%w: mirror %q", ErrUnknownReplicaSet, mirror)
			}
			if mirror == mapping.MainReplicaSet {
				return fmt.Errorf("%w: replica set %q cannot mirror itself", ErrMirrorConfiguration, mirror)
			}
			if _, ok := seenMirrors[mirror]; ok {
				return fmt.Errorf("%w: duplicate mirror %q", ErrMirrorConfiguration, mirror)
			}
			seenMirrors[mirror] = struct{}{}
		}
		configuration := strings.Join(mapping.MirrorReplicaSets, "\x00")
		if previous, ok := mirrorConfigurations[mapping.MainReplicaSet]; ok && previous != configuration {
			return fmt.Errorf("%w for %q", ErrMirrorConfiguration, mapping.MainReplicaSet)
		}
		mirrorConfigurations[mapping.MainReplicaSet] = configuration

		for _, vshard := range mapping.VShards {
			if vshard >= opts.Shards.NumVShards {
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
