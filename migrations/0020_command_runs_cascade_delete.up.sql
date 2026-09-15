-- Deleting a Quick Command now deletes its run history too (was ON DELETE
-- SET NULL — kept orphaned rows nobody could browse from the UI once their
-- command was gone). Existing orphaned rows (from a delete under the old
-- behavior) are cleaned up here as well, then the FK is tightened to CASCADE.
DELETE FROM command_runs WHERE command_id IS NULL;

ALTER TABLE command_runs DROP CONSTRAINT command_runs_command_id_fkey;
ALTER TABLE command_runs
    ADD CONSTRAINT command_runs_command_id_fkey
    FOREIGN KEY (command_id) REFERENCES project_commands(id) ON DELETE CASCADE;

ALTER TABLE command_runs ALTER COLUMN command_id SET NOT NULL;
