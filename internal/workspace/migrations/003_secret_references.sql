CREATE TABLE gometer.secret_references (
 workspace_id text NOT NULL REFERENCES gometer.workspaces(id),
 id text NOT NULL CHECK (id ~ '^[a-f0-9]{32}$'),
 name text NOT NULL CHECK (length(name) BETWEEN 1 AND 256),
 source_key text NOT NULL CHECK (source_key ~ '^[A-Za-z0-9_-][A-Za-z0-9_.-]{0,127}$'),
 revision bigint NOT NULL CHECK (revision > 0),
 enabled boolean NOT NULL DEFAULT true,
 created_by text NOT NULL REFERENCES gometer.actors(id),
 last_edited_by text NOT NULL REFERENCES gometer.actors(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (workspace_id,id)
);
CREATE UNIQUE INDEX secret_reference_names ON gometer.secret_references(workspace_id,lower(name));
