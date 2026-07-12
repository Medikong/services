-- +goose Up
CREATE TABLE reference_fenced_writes (
    resource_id text PRIMARY KEY,
    fence_token bigint NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
