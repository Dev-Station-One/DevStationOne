-- Web Push (docs/DECISIONS.md ADR-055). One VAPID keypair per installation,
-- generated on first start; the private half is sealed with
-- DEVSTATIONONE_DB_ENCRYPTION_KEY like stored database credentials.
CREATE TABLE push_vapid (
    singleton       boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    public_key      text NOT NULL,
    private_key_enc bytea NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);

-- One row per browser/device push endpoint. The endpoint URL is the push
-- service's capability URL, so it is unique across users; re-subscribing
-- from the same browser moves the row to whoever is logged in.
CREATE TABLE push_subscriptions (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    endpoint   text NOT NULL UNIQUE,
    p256dh     text NOT NULL,
    auth       text NOT NULL,
    user_agent text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX push_subscriptions_user_id_idx ON push_subscriptions(user_id);
