CREATE TABLE gometer.test_drafts (
 workspace_id text NOT NULL REFERENCES gometer.workspaces(id),
 actor_id text NOT NULL REFERENCES gometer.actors(id),
 id text NOT NULL CHECK (id ~ '^[a-f0-9]{32}$'),
 revision bigint NOT NULL CHECK (revision > 0),
 base_test_id text,
 base_revision bigint,
 document jsonb NOT NULL CHECK (jsonb_typeof(document)='object'),
 name text NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(workspace_id,actor_id,id),
 CHECK ((base_test_id IS NULL AND base_revision IS NULL) OR (base_test_id ~ '^[a-f0-9]{32}$' AND base_revision > 0))
);
CREATE INDEX test_drafts_by_author ON gometer.test_drafts(workspace_id,actor_id,updated_at DESC);
