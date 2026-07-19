-- name: GetUser :one
-- kind: read
-- shard: tenant(tenant_id)
SELECT id, tenant_id, name
FROM users
WHERE tenant_id = $1 AND id = $2;

-- name: CreateUser :one
-- kind: write
-- shard: tenant(tenant_id)
INSERT INTO users (id, tenant_id, name)
VALUES ($1, $2, $3)
RETURNING id, tenant_id, name;

-- name: ListUsers :many
-- kind: read
SELECT id, tenant_id, name
FROM users
ORDER BY id;

-- name: CopyUsers :copyfrom
-- kind: write
INSERT INTO users (id, tenant_id, name)
VALUES ($1, $2, $3);

-- name: GetAnalysis :one
-- kind: read
-- shard: tenant(tenant_id)
SELECT id, tenant_id, summary, state, source, active_window
FROM analyses
WHERE tenant_id = $1 AND id = $2;
