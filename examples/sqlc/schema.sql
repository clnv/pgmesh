CREATE TABLE accounts (
    id BIGINT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    display_name TEXT NOT NULL
);
