-- name: GetSetting :one
-- kind: read
SELECT key, value
FROM application_settings
WHERE key = $1;

-- name: UpsertSetting :one
-- kind: write
INSERT INTO application_settings (key, value)
VALUES ($1, $2)
ON CONFLICT (key) DO UPDATE
SET value = EXCLUDED.value
RETURNING key, value;
