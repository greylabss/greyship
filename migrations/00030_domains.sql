-- +goose Up
-- Custom hostnames people point at a web service (decisions.md 057). A hostname belongs to exactly one service;
-- deleting the service or its project removes it.
CREATE TABLE domains (
    hostname   citext PRIMARY KEY,
    service_id uuid NOT NULL REFERENCES services (id) ON DELETE CASCADE,
    created_by uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX domains_service ON domains (service_id);

-- +goose Down
DROP TABLE domains;
