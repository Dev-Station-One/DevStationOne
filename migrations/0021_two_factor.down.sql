DROP TABLE user_2fa_recovery_tokens;
DROP TABLE user_trusted_devices;
DROP TABLE user_email_2fa;
DROP TABLE user_backup_codes;
DROP TABLE webauthn_credentials;
DROP TABLE user_totp;
ALTER TABLE users DROP COLUMN two_factor_enrolled_at;
