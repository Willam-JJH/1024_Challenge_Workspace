-- Running upgrade eae3777bb79c -> b7c1e5a9d240

ALTER TABLE events ADD COLUMN archived_at TIMESTAMP WITHOUT TIME ZONE;

CREATE TABLE event_enrollments (
    event_id VARCHAR(32) NOT NULL, 
    user_id VARCHAR(32) NOT NULL, 
    kind VARCHAR(16) NOT NULL, 
    status VARCHAR(16) NOT NULL, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(event_id) REFERENCES events (id) ON DELETE CASCADE, 
    FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE, 
    CONSTRAINT uq_event_enrollment UNIQUE (event_id, user_id)
);

CREATE INDEX ix_event_enrollments_event_id ON event_enrollments (event_id);

CREATE INDEX ix_event_enrollments_user_id ON event_enrollments (user_id);

UPDATE alembic_version SET version_num='b7c1e5a9d240' WHERE alembic_version.version_num = 'eae3777bb79c';
