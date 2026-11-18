ALTER TABLE targets DROP CONSTRAINT targets_engine_check;
ALTER TABLE targets ADD CONSTRAINT targets_engine_check CHECK (engine IN ('postgres', 'mysql', 'mariadb'));
ALTER TABLE policies DROP CONSTRAINT policies_dump_format_check;
ALTER TABLE policies ADD CONSTRAINT policies_dump_format_check CHECK (dump_format IN ('custom', 'directory', 'sql'));
