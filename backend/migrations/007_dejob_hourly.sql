-- Apply to the platform database. The scheduler supplies its own login secret.
BEGIN;
SET LOCAL lock_timeout = '4s';
ALTER TABLE public.job_listings
  ADD COLUMN IF NOT EXISTS salary_currency text,
  ADD COLUMN IF NOT EXISTS pay_period text,
  ADD COLUMN IF NOT EXISTS has_contact boolean GENERATED ALWAYS AS (
    coalesce(nullif(trim(email), ''), nullif(trim(phone), ''), nullif(trim(wechat), ''), nullif(trim(telegram), '')) IS NOT NULL
  ) STORED,
  ADD COLUMN IF NOT EXISTS is_remote boolean GENERATED ALWAYS AS (
    coalesce(office_mode_name, '') || ' ' || coalesce(location, '') ~* '(remote|远程|线上)'
  ) STORED;

-- The latest feed exposes public descriptions; application details may remain with the employer.
-- Keep the direct-contact rule for other feeds and keep unverified rows out.
ALTER TABLE public.job_listings DROP CONSTRAINT IF EXISTS job_listings_contact_required_check;
ALTER TABLE public.job_listings ADD CONSTRAINT job_listings_contact_required_check CHECK (
  has_contact OR coalesce(source_slug='dejob' AND external_id ~ '^[0-9]+$'
    AND source_url='https://dejob.ai/jobDetail?id=' || external_id AND length(trim(title))>0, false)
);

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'zood_dejob_sync') THEN
    CREATE ROLE zood_dejob_sync LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
  END IF;
  EXECUTE format('GRANT CONNECT ON DATABASE %I TO zood_dejob_sync', current_database());
END $$;
GRANT USAGE ON SCHEMA public TO zood_dejob_sync;
GRANT SELECT (id, slug, name, is_active, sync_config), UPDATE (sync_config) ON public.job_sources TO zood_dejob_sync;
GRANT SELECT, INSERT, UPDATE ON public.job_listings, public.job_sync_runs TO zood_dejob_sync;
DROP POLICY IF EXISTS dejob_sync_source ON public.job_sources;
CREATE POLICY dejob_sync_source ON public.job_sources TO zood_dejob_sync
  USING (slug = 'dejob' AND is_active) WITH CHECK (slug = 'dejob' AND is_active);
DROP POLICY IF EXISTS dejob_sync_listings ON public.job_listings;
CREATE POLICY dejob_sync_listings ON public.job_listings TO zood_dejob_sync
  USING (source_id = (SELECT id FROM public.job_sources WHERE slug = 'dejob'))
  WITH CHECK (source_id = (SELECT id FROM public.job_sources WHERE slug = 'dejob'));
DROP POLICY IF EXISTS dejob_sync_runs ON public.job_sync_runs;
CREATE POLICY dejob_sync_runs ON public.job_sync_runs TO zood_dejob_sync
  USING (source_id = (SELECT id FROM public.job_sources WHERE slug = 'dejob'))
  WITH CHECK (source_id = (SELECT id FROM public.job_sources WHERE slug = 'dejob'));
INSERT INTO public.job_sources (slug, name, base_url)
VALUES ('dejob', 'DeJob', 'https://dejob.ai') ON CONFLICT (slug) DO NOTHING;
COMMIT;
