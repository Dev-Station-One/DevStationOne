CREATE TABLE projects (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name              text NOT NULL,
    runtime_id        uuid NOT NULL REFERENCES runtimes(id),
    os_user           text NOT NULL,
    working_directory text NOT NULL,
    shell             text NOT NULL,
    environment       jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_by        uuid NOT NULL REFERENCES users(id),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX projects_created_by_idx ON projects(created_by);
