import "base.just"

export COMPOSE := env_var_or_default("COMPOSE", "docker compose")
export COMPOSE_FILE := env_var_or_default("COMPOSE_FILE", "integration/docker-compose.yaml")
export COMPOSE_PROJECT := env_var_or_default("COMPOSE_PROJECT", "sqlcstore-integration")
export SQLCSTORE_SHARD0_PRIMARY_PORT := env_var_or_default("SQLCSTORE_SHARD0_PRIMARY_PORT", "25432")
export SQLCSTORE_SHARD0_REPLICA0_PORT := env_var_or_default("SQLCSTORE_SHARD0_REPLICA0_PORT", "25433")
export SQLCSTORE_SHARD0_REPLICA1_PORT := env_var_or_default("SQLCSTORE_SHARD0_REPLICA1_PORT", "25434")
export SQLCSTORE_SHARD1_PRIMARY_PORT := env_var_or_default("SQLCSTORE_SHARD1_PRIMARY_PORT", "25435")
export SQLCSTORE_SHARD0_MIRROR_PORT := env_var_or_default("SQLCSTORE_SHARD0_MIRROR_PORT", "25436")

# Start the local PostgreSQL topology.
integration-up:
    {{ COMPOSE }} -p {{ COMPOSE_PROJECT }} -f {{ COMPOSE_FILE }} up -d --wait --remove-orphans

# Stop the local PostgreSQL topology and remove its volumes.
integration-down:
    {{ COMPOSE }} -p {{ COMPOSE_PROJECT }} -f {{ COMPOSE_FILE }} down --volumes --remove-orphans

# Run the Docker-backed integration tests.
integration-test:
    SQLCSTORE_INTEGRATION=1 go test -race -count=1 -tags=integration -timeout=2m ./integration

# Run all executable examples against the local topology.
examples-smoke:
    SINGLE_DATABASE_DSN='postgres://sqlcstore:sqlcstore@127.0.0.1:{{ SQLCSTORE_SHARD0_PRIMARY_PORT }}/sqlcstore?sslmode=disable' go run ./examples/01-single-database
    RW_PRIMARY_DSN='postgres://sqlcstore:sqlcstore@127.0.0.1:{{ SQLCSTORE_SHARD0_PRIMARY_PORT }}/sqlcstore?sslmode=disable' RW_REPLICA_DSN='postgres://sqlcstore:sqlcstore@127.0.0.1:{{ SQLCSTORE_SHARD0_PRIMARY_PORT }}/sqlcstore?sslmode=disable' go run ./examples/02-read-write-split
    SHARD0_PRIMARY_DSN='postgres://sqlcstore:sqlcstore@127.0.0.1:{{ SQLCSTORE_SHARD0_PRIMARY_PORT }}/sqlcstore?sslmode=disable' SHARD0_REPLICA_DSN='postgres://sqlcstore:sqlcstore@127.0.0.1:{{ SQLCSTORE_SHARD0_REPLICA0_PORT }}/sqlcstore?sslmode=disable' SHARD1_PRIMARY_DSN='postgres://sqlcstore:sqlcstore@127.0.0.1:{{ SQLCSTORE_SHARD1_PRIMARY_PORT }}/sqlcstore?sslmode=disable' SHARD1_REPLICA_DSN='postgres://sqlcstore:sqlcstore@127.0.0.1:{{ SQLCSTORE_SHARD0_REPLICA1_PORT }}/sqlcstore?sslmode=disable' go run ./examples/03-sharded-read-write
    ADV_SHARD0_PRIMARY_DSN='postgres://sqlcstore:sqlcstore@127.0.0.1:{{ SQLCSTORE_SHARD0_PRIMARY_PORT }}/sqlcstore?sslmode=disable' ADV_SHARD0_REPLICA_DSN='postgres://sqlcstore:sqlcstore@127.0.0.1:{{ SQLCSTORE_SHARD0_REPLICA0_PORT }}/sqlcstore?sslmode=disable' ADV_SHARD0_MIRROR_DSN='postgres://sqlcstore:sqlcstore@127.0.0.1:{{ SQLCSTORE_SHARD0_MIRROR_PORT }}/sqlcstore?sslmode=disable' ADV_SHARD1_PRIMARY_DSN='postgres://sqlcstore:sqlcstore@127.0.0.1:{{ SQLCSTORE_SHARD1_PRIMARY_PORT }}/sqlcstore?sslmode=disable' ADV_SHARD1_REPLICA_DSN='postgres://sqlcstore:sqlcstore@127.0.0.1:{{ SQLCSTORE_SHARD0_REPLICA1_PORT }}/sqlcstore?sslmode=disable' ADV_SHARD1_MIRROR_DSN='postgres://sqlcstore:sqlcstore@127.0.0.1:{{ SQLCSTORE_SHARD0_MIRROR_PORT }}/sqlcstore?sslmode=disable' go run ./examples/04-mirrors-and-transactions

# Start, test, smoke-test, and clean up the local topology.
integration:
    #!/usr/bin/env bash
    set -eu
    cleanup() {
        "{{ just_executable() }}" integration-down
    }
    trap cleanup EXIT INT TERM
    "{{ just_executable() }}" integration-up
    "{{ just_executable() }}" integration-test
    "{{ just_executable() }}" examples-smoke
