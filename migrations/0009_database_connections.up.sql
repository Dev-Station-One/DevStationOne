CREATE TABLE database_connections (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id        uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name              text NOT NULL,
    engine            text NOT NULL CHECK (engine IN ('postgresql', 'mysql', 'mssql', 'sqlite')),
    host              text NOT NULL DEFAULT '',
    port              int NOT NULL DEFAULT 0,
    username          text NOT NULL DEFAULT '',
    database_name     text NOT NULL DEFAULT '',
    ssl_mode          text NOT NULL DEFAULT '',
    instance_name     text NOT NULL DEFAULT '',
    search_path       text NOT NULL DEFAULT '',
    encrypted_secret  bytea,
    created_by        uuid NOT NULL REFERENCES users(id),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX database_connections_project_id_idx ON database_connections(project_id);
