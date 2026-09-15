ALTER TABLE command_runs ALTER COLUMN command_id DROP NOT NULL;

ALTER TABLE command_runs DROP CONSTRAINT command_runs_command_id_fkey;
ALTER TABLE command_runs
    ADD CONSTRAINT command_runs_command_id_fkey
    FOREIGN KEY (command_id) REFERENCES project_commands(id) ON DELETE SET NULL;
