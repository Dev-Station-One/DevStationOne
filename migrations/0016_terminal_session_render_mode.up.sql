ALTER TABLE terminal_sessions ADD COLUMN render_mode text NOT NULL DEFAULT 'xterm'
    CHECK (render_mode IN ('xterm', 'cards'));
