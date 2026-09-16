-- User deletion: history rows keep existing with a NULL creator instead of
-- blocking the delete (mirrors audit_events_actor_user_id_fkey's SET NULL).
-- Active resources (projects, runtimes, backup_schedules,
-- database_connections) keep NO ACTION and are pre-checked in
-- deleteUserHandler, which names what's in the way.
ALTER TABLE journal_notes ALTER COLUMN created_by DROP NOT NULL;
ALTER TABLE command_runs ALTER COLUMN run_by DROP NOT NULL;
ALTER TABLE terminal_sessions ALTER COLUMN created_by DROP NOT NULL;
ALTER TABLE project_commands ALTER COLUMN created_by DROP NOT NULL;
ALTER TABLE backups ALTER COLUMN created_by DROP NOT NULL;

ALTER TABLE journal_notes DROP CONSTRAINT journal_notes_created_by_fkey;
ALTER TABLE journal_notes ADD CONSTRAINT journal_notes_created_by_fkey FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE SET NULL;

ALTER TABLE command_runs DROP CONSTRAINT command_runs_run_by_fkey;
ALTER TABLE command_runs ADD CONSTRAINT command_runs_run_by_fkey FOREIGN KEY (run_by) REFERENCES users (id) ON DELETE SET NULL;

ALTER TABLE terminal_sessions DROP CONSTRAINT terminal_sessions_created_by_fkey;
ALTER TABLE terminal_sessions ADD CONSTRAINT terminal_sessions_created_by_fkey FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE SET NULL;

ALTER TABLE project_commands DROP CONSTRAINT project_commands_created_by_fkey;
ALTER TABLE project_commands ADD CONSTRAINT project_commands_created_by_fkey FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE SET NULL;

ALTER TABLE backups DROP CONSTRAINT backups_created_by_fkey;
ALTER TABLE backups ADD CONSTRAINT backups_created_by_fkey FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE SET NULL;