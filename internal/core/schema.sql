CREATE TABLE IF NOT EXISTS core.cycles (
 id text PRIMARY KEY, furnace_id text NOT NULL, basket_no text NOT NULL DEFAULT '',
 status text NOT NULL, created_at timestamptz NOT NULL, doc jsonb NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS one_active_cycle ON core.cycles(furnace_id)
 WHERE status NOT IN ('COMPLETED','ABORTED');
CREATE TABLE IF NOT EXISTS core.events (
 id text PRIMARY KEY, cycle_id text NOT NULL REFERENCES core.cycles(id), at timestamptz NOT NULL, doc jsonb NOT NULL
);
CREATE TABLE IF NOT EXISTS core.commands (
 id text PRIMARY KEY, cycle_id text NOT NULL, device_id text NOT NULL,
 status text NOT NULL, attempts integer NOT NULL DEFAULT 0,
 next_attempt_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL, doc jsonb NOT NULL
);
CREATE TABLE IF NOT EXISTS core.occupancy (basket_no text PRIMARY KEY, cycle_id text NOT NULL REFERENCES core.cycles(id));
CREATE TABLE IF NOT EXISTS core.alarms (
 id text PRIMARY KEY, furnace_id text NOT NULL, cycle_id text NOT NULL, code text NOT NULL,
 active boolean NOT NULL DEFAULT true, raised_at timestamptz NOT NULL, doc jsonb NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS one_active_alarm ON core.alarms(furnace_id,code) WHERE active;
CREATE TABLE IF NOT EXISTS core.outbox (
 id text PRIMARY KEY, kind text NOT NULL, payload jsonb NOT NULL,
 attempts integer NOT NULL DEFAULT 0, next_attempt_at timestamptz NOT NULL DEFAULT now(), delivered boolean NOT NULL DEFAULT false
);
CREATE TABLE IF NOT EXISTS core.idempotency (
 scope text NOT NULL, key text NOT NULL, body jsonb NOT NULL, response jsonb,
 PRIMARY KEY(scope,key)
);
CREATE TABLE IF NOT EXISTS core.audit (
 id text PRIMARY KEY, at timestamptz NOT NULL DEFAULT now(), operator text NOT NULL,
 action text NOT NULL, target text NOT NULL, reason text NOT NULL
);
CREATE TABLE IF NOT EXISTS core.users (
 id text PRIMARY KEY, username text NOT NULL UNIQUE, display_name text NOT NULL,
 password_hash text NOT NULL, role text NOT NULL CHECK(role IN ('admin','user')),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS core.sessions (
 token_hash text PRIMARY KEY, user_id text NOT NULL REFERENCES core.users(id),
 expires_at timestamptz NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE core.users ADD COLUMN IF NOT EXISTS enabled boolean NOT NULL DEFAULT true;
ALTER TABLE core.users ADD COLUMN IF NOT EXISTS access_version bigint NOT NULL DEFAULT 0;
CREATE TABLE IF NOT EXISTS core.camera_settings (
 furnace_id text PRIMARY KEY, mode text NOT NULL DEFAULT 'simulated',
 encrypted_url bytea NOT NULL DEFAULT ''::bytea, revision bigint NOT NULL DEFAULT 1
);
CREATE TABLE IF NOT EXISTS core.batches (
 id text PRIMARY KEY, batch_no text NOT NULL UNIQUE,
 material_name text NOT NULL, process_spec text NOT NULL DEFAULT '',
 quality_status text NOT NULL CHECK(quality_status IN ('pending','passed','failed')),
 enabled boolean NOT NULL DEFAULT true, notes text NOT NULL DEFAULT '',
 version bigint NOT NULL DEFAULT 1, created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz
);
CREATE TABLE IF NOT EXISTS core.baskets (
 basket_no text PRIMARY KEY, batch_id text NOT NULL REFERENCES core.batches(id),
 quantity integer NOT NULL CHECK(quantity BETWEEN 1 AND 100000),
 enabled boolean NOT NULL DEFAULT true, notes text NOT NULL DEFAULT '',
 version bigint NOT NULL DEFAULT 1, created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz
);
CREATE INDEX IF NOT EXISTS baskets_batch ON core.baskets(batch_id);
CREATE TABLE IF NOT EXISTS core.catalog_revision (
 id integer PRIMARY KEY CHECK(id=1), version bigint NOT NULL DEFAULT 0
);
INSERT INTO core.catalog_revision(id) VALUES(1) ON CONFLICT DO NOTHING;
ALTER TABLE core.catalog_revision ADD COLUMN IF NOT EXISTS rules_version bigint NOT NULL DEFAULT 0;
CREATE TABLE IF NOT EXISTS core.alarm_rules (
 id text PRIMARY KEY, furnace_id text NOT NULL, doc jsonb NOT NULL,
 version bigint NOT NULL DEFAULT 1, created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz
);
CREATE TABLE IF NOT EXISTS core.alarm_rule_states (
 rule_id text PRIMARY KEY REFERENCES core.alarm_rules(id),
 active_alarm_id text NOT NULL DEFAULT '', notified boolean NOT NULL DEFAULT false,
 next_reminder_at timestamptz
);

ALTER TABLE core.alarm_rules ADD COLUMN IF NOT EXISTS seed_key text UNIQUE;
ALTER TABLE core.catalog_revision ADD COLUMN IF NOT EXISTS legacy_alarms_migrated boolean NOT NULL DEFAULT false;
