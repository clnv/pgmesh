package pgmesh

// ReplicaSet represents one physical shard. Reads are balanced across replica
// readers, while writes always use the primary writer and its configured
// synchronous mirrors.
type ReplicaSet[R any, W Mirrorable[W]] struct {
	name         string
	primary      Node[R, W]
	replicas     *roundRobin[Node[R, W]]
	writeMirrors []W
}

func NewReplicaSet[R any, W Mirrorable[W]](
	name string,
	primary Node[R, W],
	replicas []Node[R, W],
) *ReplicaSet[R, W] {
	if len(replicas) == 0 {
		replicas = []Node[R, W]{primary}
	}
	return &ReplicaSet[R, W]{
		name:         name,
		primary:      primary,
		replicas:     newRoundRobin(replicas),
		writeMirrors: nil,
	}
}

func (s *ReplicaSet[R, W]) Name() string {
	return s.name
}

func (s *ReplicaSet[R, W]) Read() R {
	return s.replicas.Next().Reader()
}

func (s *ReplicaSet[R, W]) Write() W {
	return s.primary.Writer().WithMirrors(s.writeMirrors...)
}

// WriteMirrorCount returns the number of synchronous write mirrors.
func (s *ReplicaSet[R, W]) WriteMirrorCount() int {
	return len(s.writeMirrors)
}

func (s *ReplicaSet[R, W]) primaryWriter() W {
	return s.primary.Writer()
}

func (s *ReplicaSet[R, W]) WithWriteMirrors(writes ...W) *ReplicaSet[R, W] {
	mirrors := append([]W(nil), s.writeMirrors...)
	mirrors = append(mirrors, writes...)
	return &ReplicaSet[R, W]{
		name:         s.name,
		primary:      s.primary,
		replicas:     s.replicas,
		writeMirrors: mirrors,
	}
}
