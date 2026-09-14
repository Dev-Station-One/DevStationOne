ALTER TABLE users ADD COLUMN display_name text NOT NULL DEFAULT '';

CREATE TABLE journal_notes (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id      uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    body_html       text NOT NULL,
    commit_sha      text NOT NULL DEFAULT '',
    commit_subject  text NOT NULL DEFAULT '',
    commit_author   text NOT NULL DEFAULT '',
    commit_datetime timestamptz,
    created_by      uuid NOT NULL REFERENCES users(id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX journal_notes_project_id_idx ON journal_notes(project_id);

CREATE TABLE journal_attachments (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    note_id      uuid NOT NULL REFERENCES journal_notes(id) ON DELETE CASCADE,
    filename     text NOT NULL,
    content_type text NOT NULL DEFAULT '',
    size_bytes   bigint NOT NULL,
    content      bytea NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX journal_attachments_note_id_idx ON journal_attachments(note_id);