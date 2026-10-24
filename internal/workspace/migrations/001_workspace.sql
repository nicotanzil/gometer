CREATE TABLE gometer.workspaces (
 id text PRIMARY KEY CHECK (id ~ '^[a-f0-9]{32}$'),
 name text NOT NULL CHECK (length(name) BETWEEN 1 AND 256),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE gometer.actors (
 id text PRIMARY KEY CHECK (id ~ '^[a-f0-9]{32}$'),
 issuer text NOT NULL,
 subject text NOT NULL,
 display_name text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (issuer, subject)
);
CREATE TABLE gometer.memberships (
 workspace_id text NOT NULL REFERENCES gometer.workspaces(id),
 actor_id text NOT NULL REFERENCES gometer.actors(id),
 role text NOT NULL CHECK (role IN ('viewer','operator','admin')),
 enabled boolean NOT NULL DEFAULT true,
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (workspace_id, actor_id)
);
CREATE TABLE gometer.test_definitions (
 workspace_id text NOT NULL REFERENCES gometer.workspaces(id),
 id text NOT NULL CHECK (id ~ '^[a-f0-9]{32}$'),
 name text NOT NULL CHECK (length(name) BETWEEN 1 AND 256),
 revision bigint NOT NULL CHECK (revision > 0),
 config jsonb NOT NULL CHECK (jsonb_typeof(config)='object'),
 created_by text NOT NULL REFERENCES gometer.actors(id),
 last_edited_by text NOT NULL REFERENCES gometer.actors(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 source_id text,
 source_revision bigint,
 CHECK ((source_id IS NULL AND source_revision IS NULL) OR (source_id ~ '^[a-f0-9]{32}$' AND source_revision > 0)),
 PRIMARY KEY (workspace_id, id)
);
CREATE INDEX test_definitions_updated ON gometer.test_definitions(workspace_id, updated_at DESC, id);
CREATE TABLE gometer.audit_events (
 sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 workspace_id text NOT NULL REFERENCES gometer.workspaces(id),
 actor_id text NOT NULL REFERENCES gometer.actors(id),
 action text NOT NULL,
 entity_id text NOT NULL,
 revision bigint,
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
