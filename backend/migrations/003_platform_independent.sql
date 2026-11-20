-- Independent platform database for phase 1.
-- Apply this only to a NEW self-hosted Postgres database.
-- Do not run it against the Supabase project, and do not point production at it
-- until PLATFORM_DATA_SOURCE is deliberately changed.

CREATE SCHEMA IF NOT EXISTS platform;

CREATE OR REPLACE FUNCTION platform.current_user_id()
RETURNS uuid
LANGUAGE sql
STABLE
AS $$
  SELECT NULLIF(current_setting('app.user_id', true), '')::uuid
$$;

ALTER TABLE IF EXISTS public.user_profiles ENABLE ROW LEVEL SECURITY;
ALTER TABLE IF EXISTS public.user_profiles FORCE ROW LEVEL SECURITY;

-- The application role must not be a superuser and must not own these tables.
-- Create it out of band and set its password only in the server environment:
--   CREATE ROLE platform_app LOGIN PASSWORD '<set on the server>';
--   GRANT CONNECT ON DATABASE platform TO platform_app;
--   GRANT USAGE ON SCHEMA public TO platform_app;
--   GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO platform_app;

DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = 'public' AND table_name = 'user_profiles'
  ) THEN
    EXECUTE 'DROP POLICY IF EXISTS platform_profile_select ON public.user_profiles';
    EXECUTE $policy$
      CREATE POLICY platform_profile_select ON public.user_profiles
      FOR SELECT
      USING (
        id = platform.current_user_id()
        OR COALESCE(current_setting('app.is_admin', true), '') = 'true'
      )
    $policy$;
    EXECUTE 'DROP POLICY IF EXISTS platform_profile_update ON public.user_profiles';
    EXECUTE $policy$
      CREATE POLICY platform_profile_update ON public.user_profiles
      FOR UPDATE
      USING (id = platform.current_user_id() OR COALESCE(current_setting('app.is_admin', true), '') = 'true')
      WITH CHECK (id = platform.current_user_id() OR COALESCE(current_setting('app.is_admin', true), '') = 'true')
    $policy$;
  END IF;
END $$;
