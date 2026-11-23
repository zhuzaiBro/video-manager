-- Local business database only. Do not run this against the remote Supabase
-- project, and do not apply it to production from this change.
--
-- Replace zood_app with the login role in PLATFORM_DATABASE_URL before running.
-- That role must own these tables. It is not superuser and does not bypass RLS.
-- The application reads them without SET ROLE. anon and authenticated have no
-- privileges, so a request-scoped query cannot read the ciphertext.
--
-- This script does not add messages to a publication and does not create a
-- platform schema.

CREATE TABLE IF NOT EXISTS public.support_settings (
  id smallint PRIMARY KEY CHECK (id = 1),
  telegram_bot_token_ciphertext bytea,
  telegram_bot_token_nonce bytea,
  telegram_bot_token_hint text,
  updated_at timestamptz NOT NULL DEFAULT now(),
  updated_by uuid
);

CREATE TABLE IF NOT EXISTS public.support_realtime_outbox (
  message_id uuid NOT NULL,
  topic text NOT NULL,
  payload jsonb NOT NULL,
  attempts integer NOT NULL DEFAULT 0,
  last_error text,
  delivered_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (message_id, topic)
);

ALTER TABLE public.support_settings OWNER TO zood_app;
ALTER TABLE public.support_realtime_outbox OWNER TO zood_app;

ALTER TABLE public.support_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.support_realtime_outbox ENABLE ROW LEVEL SECURITY;

REVOKE ALL ON public.support_settings FROM PUBLIC, anon, authenticated;
REVOKE ALL ON public.support_realtime_outbox FROM PUBLIC, anon, authenticated;

INSERT INTO public.support_settings (id) VALUES (1) ON CONFLICT (id) DO NOTHING;
