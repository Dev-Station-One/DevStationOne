CREATE TABLE audit_events (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    occurred_at    timestamptz NOT NULL DEFAULT now(),
    event_type     text NOT NULL,
    actor_user_id  uuid REFERENCES users(id) ON DELETE SET NULL,
    actor_email    text NOT NULL DEFAULT '',
    resource_type  text NOT NULL DEFAULT '',
    resource_id    text NOT NULL DEFAULT '',
    detail         jsonb NOT NULL DEFAULT '{}'::jsonb,
    ip_address     text NOT NULL DEFAULT ''
);

CREATE INDEX audit_events_occurred_at_idx ON audit_events(occurred_at DESC);
CREATE INDEX audit_events_event_type_idx ON audit_events(event_type);
CREATE INDEX audit_events_actor_user_id_idx ON audit_events(actor_user_id);

CREATE TABLE audit_settings (
    id             integer PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    retention_days integer NOT NULL DEFAULT 90,
    updated_at     timestamptz NOT NULL DEFAULT now()
);

INSERT INTO audit_settings (id, retention_days) VALUES (1, 90);
