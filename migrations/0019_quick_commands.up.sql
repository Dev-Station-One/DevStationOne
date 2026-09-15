-- project_commands holds a Project's Quick Commands (ADR-045): named CLI
-- commands a project member can run with one click, each opening a real PTY
-- session on the project's runtime.
CREATE TABLE project_commands (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id  uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name        text NOT NULL,
    command     text NOT NULL,
    sort_order  integer NOT NULL DEFAULT 0,
    created_by  uuid NOT NULL REFERENCES users(id),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX project_commands_project_id_idx ON project_commands(project_id, sort_order);

-- command_runs is the run history for a Project's Quick Commands, including
-- the last 64 KiB of a run's PTY output (ADR-045's bounded exception to
-- ADR-007). command_id is nullable with ON DELETE SET NULL so deleting a
-- command never deletes its history; name/command_text snapshot the command
-- at run time, since the command row itself can change or vanish.
CREATE TABLE command_runs (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id   uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    command_id   uuid REFERENCES project_commands(id) ON DELETE SET NULL,
    name         text NOT NULL,
    command_text text NOT NULL,
    session_id   uuid REFERENCES terminal_sessions(id) ON DELETE SET NULL,
    run_by       uuid NOT NULL REFERENCES users(id),
    started_at   timestamptz NOT NULL DEFAULT now(),
    finished_at  timestamptz,
    exit_code    integer,
    error        text NOT NULL DEFAULT '',
    output_tail  text NOT NULL DEFAULT ''
);

CREATE INDEX command_runs_project_id_idx ON command_runs(project_id, started_at DESC);
CREATE INDEX command_runs_command_id_idx ON command_runs(command_id, started_at DESC);
CREATE INDEX command_runs_session_id_idx ON command_runs(session_id);