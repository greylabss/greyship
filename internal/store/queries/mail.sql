-- name: GetMailSettings :one
SELECT enabled, api_key_enc, sending_domain, from_name FROM mail_settings WHERE id;

-- name: SaveMailSettings :exec
INSERT INTO mail_settings (id, enabled, api_key_enc, sending_domain, from_name)
VALUES (true, @enabled, sqlc.narg('api_key_enc')::bytea, @sending_domain, @from_name)
ON CONFLICT (id) DO UPDATE SET
    enabled = EXCLUDED.enabled,
    api_key_enc = coalesce(EXCLUDED.api_key_enc, mail_settings.api_key_enc),
    sending_domain = EXCLUDED.sending_domain,
    from_name = EXCLUDED.from_name,
    updated_at = now();
