-- +goose Up
-- Email settings the instance admin sets in the dashboard (decisions.md 052). At most one row. Once it exists it
-- replaces the GREYSHIP_RESEND_API_KEY environment setting. The API key is sealed with the master key.
CREATE TABLE mail_settings (
    id             boolean PRIMARY KEY DEFAULT true CHECK (id),
    enabled        boolean NOT NULL DEFAULT false,
    api_key_enc    bytea,
    sending_domain text NOT NULL DEFAULT '',
    from_name      text NOT NULL DEFAULT 'Greyship',
    updated_at     timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE mail_settings;
