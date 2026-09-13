-- terminal_sessions holds persistent PTY session metadata. Named distinctly
-- from the existing "sessions" table (browser auth sessions, see
-- 0002_users_sessions.up.sql) to avoid confusion between the two unrelated
-- concepts that the product both calls "sessions".
CREATE TABLE terminal_sessions (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id    uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name          text NOT NULL,
    status        text NOT NULL DEFAULT 'requested',
    cols          integer NOT NULL DEFAULT 80,
    rows          integer NOT NULL DEFAULT 24,
    exit_code     integer,
    error_message text,
    created_by    uuid NOT NULL REFERENCES users(id),
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    closed_at     timestamptz
);

CREATE INDEX terminal_sessions_project_id_idx ON terminal_sessions(project_id);
