-- Mandatory 2FA + passkeys (docs/DECISIONS.md ADR-047). NULL
-- two_factor_enrolled_at forces every existing account through the
-- enrollment gate on its next login; new accounts start NULL too, so the
-- gate applies permanently, not just to the rollout.
ALTER TABLE users ADD COLUMN two_factor_enrolled_at timestamptz;

CREATE TABLE user_totp (
    user_id          uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    encrypted_secret bytea NOT NULL,
    confirmed_at     timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE webauthn_credentials (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credential_id bytea NOT NULL UNIQUE,
    name          text NOT NULL,
    resident_key  boolean NOT NULL,
    credential    jsonb NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_used_at  timestamptz
);

CREATE INDEX webauthn_credentials_user_id_idx ON webauthn_credentials(user_id);

CREATE TABLE user_backup_codes (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash  text NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX user_backup_codes_user_id_idx ON user_backup_codes(user_id);

CREATE TABLE user_email_2fa (
    user_id    uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    enabled_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE user_trusted_devices (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    user_agent text NOT NULL DEFAULT '',
    ip_address text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);

CREATE INDEX user_trusted_devices_user_id_idx ON user_trusted_devices(user_id);

CREATE TABLE user_2fa_recovery_tokens (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    used_at    timestamptz
);
