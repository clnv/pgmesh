CREATE TABLE accounts (
    id BIGINT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    display_name TEXT NOT NULL
);

CREATE TABLE application_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT INTO application_settings (key, value)
VALUES ('deployment_name', 'pgmesh example');
