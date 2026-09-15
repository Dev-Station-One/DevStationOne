CREATE TABLE backups (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind          text NOT NULL CHECK (kind IN ('db', 'project')),
    connection_id uuid REFERENCES database_connections(id) ON DELETE CASCADE,
    project_id    uuid REFERENCES projects(id)             ON DELETE CASCADE,
    runtime_id    uuid NOT NULL REFERENCES runtimes(id),
    file_name     text NOT NULL,
    size_bytes    bigint NOT NULL DEFAULT 0,
    trigger       text NOT NULL CHECK (trigger IN ('manual', 'schedule', 'pre_restore')),
    state         text NOT NULL DEFAULT 'running' CHECK (state IN ('running', 'done', 'failed')),
    error         text,
    created_by    uuid NOT NULL REFERENCES users(id),
    created_at    timestamptz NOT NULL DEFAULT now(),
    CHECK ((kind = 'db' AND connection_id IS NOT NULL AND project_id IS NULL)
        OR (kind = 'project' AND connection_id IS NULL AND project_id IS NOT NULL))
);

CREATE INDEX backups_connection_id_idx ON backups(connection_id);
CREATE INDEX backups_project_id_idx ON backups(project_id);

CREATE TABLE backup_schedules (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind          text NOT NULL CHECK (kind IN ('db', 'project')),
    connection_id uuid UNIQUE REFERENCES database_connections(id) ON DELETE CASCADE,
    project_id    uuid UNIQUE REFERENCES projects(id)             ON DELETE CASCADE,
    cadence       text NOT NULL CHECK (cadence IN ('hourly', 'daily', 'weekly')),
    retention     int NOT NULL DEFAULT 10 CHECK (retention BETWEEN 1 AND 100),
    next_run_at   timestamptz NOT NULL,
    last_run_at   timestamptz,
    last_result   text CHECK (last_result IN ('success', 'failed', 'skipped_offline')),
    last_error    text,
    created_by    uuid NOT NULL REFERENCES users(id),
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CHECK ((kind = 'db' AND connection_id IS NOT NULL AND project_id IS NULL)
        OR (kind = 'project' AND connection_id IS NULL AND project_id IS NOT NULL))
);