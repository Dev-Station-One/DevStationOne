CREATE TABLE runtimes (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name            text NOT NULL,
    token_hash      text NOT NULL UNIQUE,
    hostname        text NOT NULL DEFAULT '',
    os              text NOT NULL DEFAULT '',
    arch            text NOT NULL DEFAULT '',
    runtime_version text NOT NULL DEFAULT '',
    capabilities    jsonb NOT NULL DEFAULT '{}'::jsonb,
    status          text NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'online', 'offline', 'revoked')),
    created_by      uuid NOT NULL REFERENCES users(id),
    enrolled_at     timestamptz,
    last_seen_at    timestamptz,
    revoked_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX runtimes_status_idx ON runtimes(status);
