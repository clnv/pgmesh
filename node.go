package pgmesh

// Mirrorable is implemented by generated primary-capable query wrappers.
// WithMirrors must return a new value and leave the receiver unchanged.
type Mirrorable[W any] interface {
	WithMirrors(...W) W
}

// Node contains the read-only and primary-capable views of one database
// connection. ReplicaSet exposes only Reader for replicas and Writer for the
// primary, preventing writes from accidentally being routed to replicas.
type Node[R any, W Mirrorable[W]] struct {
	reader R
	writer W
}

func NewNode[R any, W Mirrorable[W]](reader R, writer W) Node[R, W] {
	return Node[R, W]{reader: reader, writer: writer}
}

func (n Node[R, W]) Reader() R {
	return n.reader
}

func (n Node[R, W]) Writer() W {
	return n.writer
}
