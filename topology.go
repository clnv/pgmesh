package pgmesh

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type Connection struct {
	DSN string
}

type ReplicaSetSpec struct {
	Name     string
	Primary  Connection
	Replicas []Connection
}

type Shards struct {
	NumVShards uint64
	Mappings   []VShardMapping
}

type VShardMapping struct {
	VShards           []uint64
	MainReplicaSet    string
	MirrorReplicaSets []string
}

type Options[R any, W Mirrorable[W], SK any] struct {
	ReplicaSets []ReplicaSetSpec
	Shards      Shards

	CreateNode     func(context.Context, string) (Node[R, W], error)
	ShardHasher    ShardHasher[SK]
	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider
}

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
		WithMeterProvider(opts.MeterProvider)
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
