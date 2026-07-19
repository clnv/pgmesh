package pgmesh

import (
	"fmt"
	"log/slog"
	"strings"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type Builder[R any, W Mirrorable[W], SK any] struct {
	vshards   []*ReplicaSet[R, W]
	hasher    ShardHasher[SK]
	telemetry queryTelemetry
	err       error
}

func NewBuilder[R any, W Mirrorable[W], SK any](numVShards uint64) *Builder[R, W, SK] {
	telemetry, telemetryErr := newQueryTelemetry(nil, nil)
	return &Builder[R, W, SK]{
		vshards:   make([]*ReplicaSet[R, W], numVShards),
		hasher:    nil,
		telemetry: telemetry,
		err:       telemetryErr,
	}
}

// WithTracerProvider configures the provider used for routed query spans.
// A nil provider uses the global OpenTelemetry tracer provider.
func (b *Builder[R, W, SK]) WithTracerProvider(provider trace.TracerProvider) *Builder[R, W, SK] {
	b.telemetry.setTracerProvider(provider)
	return b
}

// WithMeterProvider configures the provider used for routed query metrics.
// A nil provider uses the global OpenTelemetry meter provider.
func (b *Builder[R, W, SK]) WithMeterProvider(provider metric.MeterProvider) *Builder[R, W, SK] {
	if err := b.telemetry.setMeterProvider(provider); err != nil && b.err == nil {
		b.err = fmt.Errorf("configure OpenTelemetry metrics: %w", err)
	}
	return b
}

// WithLogger configures optional structured logging for routed queries.
// Completed queries are logged at Debug level. A nil logger disables logging.
func (b *Builder[R, W, SK]) WithLogger(logger *slog.Logger) *Builder[R, W, SK] {
	b.telemetry.logger = logger
	return b
}

func (b *Builder[R, W, SK]) WithHasher(hasher ShardHasher[SK]) *Builder[R, W, SK] {
	b.hasher = hasher
	return b
}

// Link records validation failures and returns the builder so topology setup
// remains fluent without panics. Build returns the first recorded error.
func (b *Builder[R, W, SK]) Link(vshard uint64, rs *ReplicaSet[R, W]) *Builder[R, W, SK] {
	if b.err != nil {
		return b
	}
	if vshard >= uint64(len(b.vshards)) {
		b.err = fmt.Errorf("%w: %d", ErrVShardOutOfRange, vshard)
		return b
	}
	if rs == nil {
		b.err = fmt.Errorf("%w: vshard %d", ErrNilReplicaSet, vshard)
		return b
	}
	if b.vshards[vshard] != nil {
		b.err = fmt.Errorf("%w: %d", ErrDuplicateVShard, vshard)
		return b
	}
	b.vshards[vshard] = rs
	return b
}

func (b *Builder[R, W, SK]) Build() (*Mesh[R, W, SK], error) {
	if b.err != nil {
		return nil, b.err
	}
	if len(b.vshards) == 0 {
		return nil, ErrNoVShards
	}
	if b.hasher == nil {
		return nil, ErrNoShardHasher
	}

	vshards := make([]virtualShard[R, W], len(b.vshards))
	physical := make([]*Shard[R, W], 0)
	seen := make(map[*ReplicaSet[R, W]]struct{})
	seenNames := make(map[string]*ReplicaSet[R, W])
	for index, rs := range b.vshards {
		if rs == nil {
			return nil, fmt.Errorf("%w: %d", ErrMissingVShard, index)
		}
		if strings.TrimSpace(rs.Name()) == "" {
			return nil, fmt.Errorf("%w: vshard %d", ErrEmptyReplicaSetName, index)
		}
		if previous, ok := seenNames[rs.Name()]; ok && previous != rs {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateReplicaSet, rs.Name())
		}
		seenNames[rs.Name()] = rs
		vshards[index] = virtualShard[R, W]{index: uint64(index), replicaSet: rs}
		if _, ok := seen[rs]; !ok {
			seen[rs] = struct{}{}
			physical = append(physical, &Shard[R, W]{vshardIndex: uint64(index), ReplicaSet: rs})
		}
	}

	return &Mesh[R, W, SK]{
		vshards:   vshards,
		physical:  physical,
		hasher:    b.hasher,
		telemetry: b.telemetry,
	}, nil
}
