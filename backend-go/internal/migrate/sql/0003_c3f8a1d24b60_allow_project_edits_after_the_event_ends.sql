-- Running upgrade b7c1e5a9d240 -> c3f8a1d24b60

ALTER TABLE events ADD COLUMN post_event_edits BOOLEAN DEFAULT true NOT NULL;

ALTER TABLE events ALTER COLUMN post_event_edits DROP DEFAULT;

UPDATE alembic_version SET version_num='c3f8a1d24b60' WHERE alembic_version.version_num = 'b7c1e5a9d240';
