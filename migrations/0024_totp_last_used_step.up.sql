ALTER TABLE user_totp ADD COLUMN last_used_step bigint NOT NULL DEFAULT 0;
