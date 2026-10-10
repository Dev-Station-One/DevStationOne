-- Paired native-client devices with per-request ECDSA signature auth
-- (docs/DECISIONS.md ADR-053). A device belongs to the user the pairing
-- CLI named and authenticates as that user; revoking it stops it cold.
CREATE TABLE devices (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         text NOT NULL,
    public_key   text NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    revoked_at   timestamptz
);

CREATE INDEX devices_user_id_idx ON devices(user_id);

-- One-time pairing codes minted by `devstationone-admin pair`; the phone
-- exchanges the raw code for a device row. Hashed like every other
-- bearer-ish credential; 10 minute TTL, single use.
CREATE TABLE device_pairing_codes (
    code_hash   text PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_name text NOT NULL,
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz
);