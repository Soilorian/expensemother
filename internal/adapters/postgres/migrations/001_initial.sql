CREATE TABLE IF NOT EXISTS users (
 id uuid PRIMARY KEY, username text UNIQUE, email text UNIQUE, phone text UNIQUE,
 telegram_id bigint UNIQUE, oidc_subject text UNIQUE, data jsonb NOT NULL
);
CREATE TABLE IF NOT EXISTS groups (id uuid PRIMARY KEY, chat_id bigint UNIQUE, data jsonb NOT NULL);
CREATE TABLE IF NOT EXISTS expenses (
 id uuid PRIMARY KEY, group_id uuid REFERENCES groups(id), parent_id uuid REFERENCES expenses(id),
 created_at timestamptz NOT NULL, data jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS expenses_group_created ON expenses(group_id, created_at DESC);
CREATE INDEX IF NOT EXISTS expenses_parent ON expenses(parent_id);
CREATE TABLE IF NOT EXISTS receipts (
 id uuid PRIMARY KEY, group_id uuid REFERENCES groups(id), owner_id uuid NOT NULL REFERENCES users(id),
 data jsonb NOT NULL
);
CREATE TABLE IF NOT EXISTS auth_proofs (key text PRIMARY KEY, value text NOT NULL, expires_at timestamptz NOT NULL);
CREATE INDEX IF NOT EXISTS auth_proofs_expiry ON auth_proofs(expires_at);
