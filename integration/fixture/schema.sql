CREATE TABLE users (
    id BIGINT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    name TEXT NOT NULL
);

CREATE TYPE analysis_state AS ENUM ('pending', 'complete');

CREATE TABLE analyses (
    id BIGINT PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    summary TEXT,
    state analysis_state,
    source INET NOT NULL,
    active_window TSTZRANGE NOT NULL
);
