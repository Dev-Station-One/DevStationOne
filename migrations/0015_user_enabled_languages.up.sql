ALTER TABLE users ADD COLUMN enabled_languages text[] NOT NULL DEFAULT ARRAY['JavaScript','TypeScript','Go','Python','HTML','CSS','JSON','YAML','Markdown','SQL','Shell']::text[];
