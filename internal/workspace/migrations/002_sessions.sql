CREATE TABLE gometer.pending_logins (
 workspace_id text NOT NULL REFERENCES gometer.workspaces(id),
 browser_hash text NOT NULL CHECK (browser_hash ~ '^[a-f0-9]{64}$'),
 sealed_flow bytea NOT NULL CHECK (octet_length(sealed_flow) BETWEEN 1 AND 16384),
 expires_at timestamptz NOT NULL,
 PRIMARY KEY (workspace_id, browser_hash)
);
CREATE INDEX pending_logins_expiry ON gometer.pending_logins(workspace_id, expires_at);
CREATE TABLE gometer.sessions (
 id text NOT NULL UNIQUE CHECK (id ~ '^[a-f0-9]{32}$'),
 workspace_id text NOT NULL,
 actor_id text NOT NULL,
 token_hash text PRIMARY KEY CHECK (token_hash ~ '^[a-f0-9]{64}$'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 expires_at timestamptz NOT NULL,
 FOREIGN KEY (workspace_id, actor_id) REFERENCES gometer.memberships(workspace_id, actor_id)
);
CREATE INDEX sessions_expiry ON gometer.sessions(workspace_id, expires_at);
