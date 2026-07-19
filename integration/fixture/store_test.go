package fixture

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/clnv/pgmesh"
)

type callLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *callLog) add(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, name)
}

func (l *callLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.calls...)
}

type fakeDB struct {
	name   string
	log    *callLog
	rowErr error
}

func (db *fakeDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	db.log.add(db.name)
	return pgconn.CommandTag{}, nil
}

func (db *fakeDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	db.log.add(db.name)
	return nil, errors.New("fake rows are not configured")
}

func (db *fakeDB) QueryRow(context.Context, string, ...any) pgx.Row {
	db.log.add(db.name)
	return fakeRow{err: db.rowErr}
}

func (db *fakeDB) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	db.log.add(db.name)
	return 1, nil
}

type fakeRow struct {
	err error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) == 3 {
		id, ok := dest[0].(*int64)
		if !ok {
			return fmt.Errorf("destination 0 has type %T, want *int64", dest[0])
		}
		tenantID, ok := dest[1].(*int64)
		if !ok {
			return fmt.Errorf("destination 1 has type %T, want *int64", dest[1])
		}
		name, ok := dest[2].(*string)
		if !ok {
			return fmt.Errorf("destination 2 has type %T, want *string", dest[2])
		}
		*id = 10
		*tenantID = 20
		*name = "user"
	}
	return nil
}

type fakeTx struct {
	*fakeDB
}

func (tx *fakeTx) Begin(context.Context) (pgx.Tx, error) {
	return tx, nil
}

func (tx *fakeTx) Commit(context.Context) error {
	return nil
}

func (tx *fakeTx) Rollback(context.Context) error {
	return nil
}

func (tx *fakeTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	return nil
}

func (tx *fakeTx) LargeObjects() pgx.LargeObjects {
	return pgx.LargeObjects{}
}

func (tx *fakeTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}

func (tx *fakeTx) Conn() *pgx.Conn {
	return nil
}

type tenantResolver struct{}

func (tenantResolver) Tenant(int64) uint64 {
	return 0
}

func buildTestStore(t *testing.T, primary, replica *fakeDB, mirrors ...*fakeDB) *ShardedQueries[uint64] {
	t.Helper()

	primaryNode := NewStoreNode(primary)
	replicaSet := sqlcstore.NewReplicaSet(
		"main",
		primaryNode,
		[]sqlcstore.Node[*ReadQueries, *StoreQueries]{NewStoreNode(replica)},
	)
	for _, mirror := range mirrors {
		if mirror != nil {
			replicaSet = replicaSet.WithWriteMirrors(NewStoreNode(mirror).Writer())
		}
	}
	mesh, err := sqlcstore.NewBuilder[*ReadQueries, *StoreQueries, uint64](1).
		WithHasher(sqlcstore.ConstantShardHashFor[uint64](0)).
		Link(0, replicaSet).
		Build()
	require.NoError(t, err)
	return NewShardedQueries(mesh, tenantResolver{})
}

func TestShardedQueriesSelectReadEndpoints(t *testing.T) {
	t.Parallel()

	log := &callLog{}
	store := buildTestStore(
		t,
		&fakeDB{name: "primary", log: log},
		&fakeDB{name: "replica", log: log},
	)

	_, err := store.GetUser(t.Context(), &GetUserParams{TenantID: 1, ID: 2})
	require.NoError(t, err)
	_, err = store.GetUser(t.Context(), &GetUserParams{TenantID: 1, ID: 2}, ReadFromPrimary())
	require.NoError(t, err)
	assert.Equal(t, []string{"replica", "primary"}, log.snapshot())
}

func TestShardedQueriesMirrorWritesAndIgnoreMissingRows(t *testing.T) {
	t.Parallel()

	log := &callLog{}
	store := buildTestStore(
		t,
		&fakeDB{name: "primary", log: log},
		&fakeDB{name: "replica", log: log},
		&fakeDB{name: "mirror", log: log, rowErr: sql.ErrNoRows},
		&fakeDB{name: "mirror-after-missing", log: log},
	)

	user, err := store.CreateUser(t.Context(), &CreateUserParams{ID: 1, TenantID: 2, Name: "user"})
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.Equal(t, []string{"primary", "mirror", "mirror-after-missing"}, log.snapshot())
}

func TestShardedQueriesReturnMirrorErrors(t *testing.T) {
	t.Parallel()

	log := &callLog{}
	mirrorErr := errors.New("mirror unavailable")
	store := buildTestStore(
		t,
		&fakeDB{name: "primary", log: log},
		&fakeDB{name: "replica", log: log},
		&fakeDB{name: "mirror", log: log, rowErr: mirrorErr},
		&fakeDB{name: "mirror-not-called", log: log},
	)

	user, err := store.CreateUser(t.Context(), &CreateUserParams{ID: 1, TenantID: 2, Name: "user"})
	require.ErrorIs(t, err, mirrorErr)
	require.NotNil(t, user, "primary result must be retained when a mirror fails")
	assert.Equal(t, []string{"primary", "mirror"}, log.snapshot())
}

func TestShardedQueriesMirrorWritesInOrder(t *testing.T) {
	t.Parallel()

	log := &callLog{}
	store := buildTestStore(
		t,
		&fakeDB{name: "primary", log: log},
		&fakeDB{name: "replica", log: log},
		&fakeDB{name: "mirror0", log: log},
		&fakeDB{name: "mirror1", log: log},
	)

	_, err := store.CreateUser(t.Context(), &CreateUserParams{ID: 1, TenantID: 2, Name: "user"})
	require.NoError(t, err)
	assert.Equal(t, []string{"primary", "mirror0", "mirror1"}, log.snapshot())
}

func TestShardedQueriesSkipMirrorsAfterPrimaryError(t *testing.T) {
	t.Parallel()

	log := &callLog{}
	primaryErr := errors.New("primary unavailable")
	store := buildTestStore(
		t,
		&fakeDB{name: "primary", log: log, rowErr: primaryErr},
		&fakeDB{name: "replica", log: log},
		&fakeDB{name: "mirror", log: log},
	)

	user, err := store.CreateUser(t.Context(), &CreateUserParams{ID: 1, TenantID: 2, Name: "user"})
	require.ErrorIs(t, err, primaryErr)
	assert.Nil(t, user)
	assert.Equal(t, []string{"primary"}, log.snapshot())
}

func TestShardedQueriesTransactionPinsPrimaryAndDropsMirrors(t *testing.T) {
	t.Parallel()

	log := &callLog{}
	store := buildTestStore(
		t,
		&fakeDB{name: "primary", log: log},
		&fakeDB{name: "replica", log: log},
		&fakeDB{name: "mirror", log: log},
	)
	tx := &fakeTx{fakeDB: &fakeDB{name: "tx", log: log}}

	_, err := store.GetUser(t.Context(), &GetUserParams{TenantID: 1, ID: 2}, WithTx(tx))
	require.NoError(t, err)
	_, err = store.CreateUser(t.Context(), &CreateUserParams{ID: 1, TenantID: 2, Name: "user"}, WithTx(tx))
	require.NoError(t, err)
	assert.Equal(t, []string{"tx", "tx"}, log.snapshot())
}
