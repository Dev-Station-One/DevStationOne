-- Restores the NO ACTION delete rule on the history columns (any NULLs
-- already produced by user deletions are kept; making the columns NOT NULL
-- again would fail until those rows are resolved).
ALTER TABLE journal_notes DROP CONSTRAINT journal_notes_created_by_fkey;
ALTER TABLE journal_notes ADD CONSTRAINT journal_notes_created_by_fkey FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE NO ACTION;

ALTER TABLE command_runs DROP CONSTRAINT command_runs_run_by_fkey;
ALTER TABLE command_runs ADD CONSTRAINT command_runs_run_by_fkey FOREIGN KEY (run_by) REFERENCES users (id) ON DELETE NO ACTION;

ALTER TABLE terminal_sessions DROP CONSTRAINT terminal_sessions_created_by_fkey;
ALTER TABLE terminal_sessions ADD CONSTRAINT terminal_sessions_created_by_fkey FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE NO ACTION;

ALTER TABLE project_commands DROP CONSTRAINT project_commands_created_by_fkey;
ALTER TABLE project_commands ADD CONSTRAINT project_commands_created_by_fkey FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE NO ACTION;

ALTER TABLE backups DROP CONSTRAINT backups_created_by_fkey;
ALTER TABLE backups ADD CONSTRAINT backups_created_by_fkey FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE NO ACTION;