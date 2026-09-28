CREATE TABLE IF NOT EXISTS notify.jobs (
 id text PRIMARY KEY, idempotency_key text NOT NULL UNIQUE, request jsonb NOT NULL,
 status text NOT NULL DEFAULT 'pending', attempts integer NOT NULL DEFAULT 0,
 last_error text NOT NULL DEFAULT '', sent_at timestamptz,
 next_attempt_at timestamptz NOT NULL DEFAULT now(), created_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE notify.jobs ADD COLUMN IF NOT EXISTS user_id text;
ALTER TABLE notify.jobs ADD COLUMN IF NOT EXISTS chat_id bigint;
ALTER TABLE notify.jobs ADD COLUMN IF NOT EXISTS binding_id text;
ALTER TABLE notify.jobs ADD COLUMN IF NOT EXISTS event_id text;
UPDATE notify.jobs SET status='cancelled',last_error='旧版通知没有个人接收者，已停止自动投递'
 WHERE user_id IS NULL AND status='pending';
CREATE TABLE IF NOT EXISTS notify.events (
 id text PRIMARY KEY, idempotency_key text NOT NULL UNIQUE, request jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS notify.preferences (user_id text PRIMARY KEY, doc jsonb NOT NULL);
CREATE TABLE IF NOT EXISTS notify.bindings (
 user_id text PRIMARY KEY, binding_id text NOT NULL UNIQUE, chat_id bigint NOT NULL UNIQUE,
 display_name text NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS notify.pair_codes (
 chat_id bigint PRIMARY KEY, code_hash text NOT NULL UNIQUE, update_id bigint NOT NULL,
 display_name text NOT NULL, expires_at timestamptz NOT NULL,
 used_by text, binding_id text
);
CREATE TABLE IF NOT EXISTS notify.bot_state (name text PRIMARY KEY,value text NOT NULL);
-- One-time expansion for existing subscribers who selected all previous categories.
-- The marker prevents overriding a later explicit opt-out from temperature alerts.
WITH migration AS (
 INSERT INTO notify.bot_state(name,value) VALUES('migration:temperature-category:v1','done')
 ON CONFLICT DO NOTHING RETURNING name
)
UPDATE notify.preferences SET doc=jsonb_set(doc,'{categories}',(doc->'categories') || '["temperature"]'::jsonb)
WHERE EXISTS(SELECT 1 FROM migration)
 AND doc->'categories' @> '["scan","mes","device","workflow"]'::jsonb
 AND NOT doc->'categories' @> '["temperature"]'::jsonb;
CREATE TABLE IF NOT EXISTS notify.account_access (
 user_id text PRIMARY KEY, enabled boolean NOT NULL, version bigint NOT NULL
);
CREATE TABLE IF NOT EXISTS notify.closed_rule_alarms (alarm_id text PRIMARY KEY);
WITH migration AS (
 INSERT INTO notify.bot_state(name,value) VALUES('migration:rules-category:v1','done')
 ON CONFLICT DO NOTHING RETURNING name
)
UPDATE notify.preferences SET doc=jsonb_set(doc,'{categories}',(doc->'categories') || '["rules"]'::jsonb)
WHERE EXISTS(SELECT 1 FROM migration)
 AND doc->'categories' @> '["scan","mes","device","workflow","temperature"]'::jsonb
 AND NOT doc->'categories' @> '["rules"]'::jsonb;
