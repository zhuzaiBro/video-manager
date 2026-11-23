-- The private service role belongs exclusively to Video API.
BEGIN;
SET LOCAL lock_timeout='4s';
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='zood_support_api') THEN
    CREATE ROLE zood_support_api LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  EXECUTE format('GRANT CONNECT ON DATABASE %I TO zood_support_api',current_database());
END $$;
CREATE SCHEMA IF NOT EXISTS support;
REVOKE ALL ON SCHEMA support FROM PUBLIC,anon,authenticated;
GRANT USAGE ON SCHEMA support,public TO zood_support_api;
CREATE TABLE IF NOT EXISTS support.telegram_bot (
  singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
  bot_id bigint NOT NULL, username text NOT NULL, encrypted_token bytea NOT NULL,
  bind_hash bytea, bind_expires timestamptz
);
CREATE TABLE IF NOT EXISTS support.telegram_bindings (
  bot_id bigint NOT NULL, chat_id bigint NOT NULL, user_id bigint NOT NULL,
  bound_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(bot_id,chat_id)
);
CREATE TABLE IF NOT EXISTS support.telegram_deliveries (
  bot_id bigint NOT NULL, chat_id bigint NOT NULL, site_id uuid NOT NULL REFERENCES public.messages(id) ON DELETE CASCADE,
  tg_message_id bigint, retry_at timestamptz, attempts integer NOT NULL DEFAULT 0,
  PRIMARY KEY (bot_id,chat_id,site_id), UNIQUE(bot_id,chat_id,tg_message_id)
);
CREATE TABLE IF NOT EXISTS support.telegram_updates (
  bot_id bigint NOT NULL, update_id bigint NOT NULL, PRIMARY KEY(bot_id,update_id)
);
GRANT SELECT, INSERT, UPDATE ON ALL TABLES IN SCHEMA support TO zood_support_api;
GRANT SELECT, INSERT(id,session_id,sender_type,content) ON public.messages TO zood_support_api;
GRANT SELECT(id) ON public.user_profiles TO zood_support_api;
DROP POLICY IF EXISTS support_read_messages ON public.messages;
CREATE POLICY support_read_messages ON public.messages FOR SELECT TO zood_support_api USING (true);
DROP POLICY IF EXISTS support_reply ON public.messages;
CREATE POLICY support_reply ON public.messages FOR INSERT TO zood_support_api WITH CHECK (sender_type='admin' AND user_id IS NULL);
DROP POLICY IF EXISTS support_member_ids ON public.user_profiles;
CREATE POLICY support_member_ids ON public.user_profiles FOR SELECT TO zood_support_api USING (true);
CREATE INDEX IF NOT EXISTS support_messages_thread_date ON public.messages(session_id,created_at DESC,id DESC);
COMMIT;
