-- Explicit job assignment for interview collections.
--
-- Run once, by a person, on the business database zood_restore_20261120.
-- Do not run it against the remote Supabase project. This script does not
-- modify 003, 004, or 005, and it does not UPDATE or DELETE existing
-- collections, questions, or tags.
--
-- Programmer's current default list is the collections tagged hot. Those rows
-- are copied below. Collections that are only on web3, web3Backend, contract,
-- or web3FE are not copied, so the smart-contract collection stays off the
-- programmer default list. The other five jobs are left empty.
--
-- Rollback drops only this new table. Existing questions stay:
--
--   DROP TABLE IF EXISTS public.interview_collection_roles;

CREATE TABLE public.interview_collection_roles (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  collection_id uuid NOT NULL REFERENCES public.interview_collections (id) ON DELETE CASCADE,
  role_slug text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT interview_collection_roles_role_slug_check CHECK (
    role_slug IN (
      'programmer',
      'design',
      'cross-border-ops',
      'crypto-bd',
      'cross-border-bd',
      'crypto-ops'
    )
  ),
  CONSTRAINT interview_collection_roles_role_collection_key UNIQUE (role_slug, collection_id)
);

CREATE INDEX interview_collection_roles_collection_id_idx
  ON public.interview_collection_roles (collection_id);

ALTER TABLE public.interview_collection_roles ENABLE ROW LEVEL SECURITY;

GRANT SELECT ON public.interview_collection_roles TO anon, authenticated;
GRANT INSERT, UPDATE, DELETE ON public.interview_collection_roles TO authenticated;
GRANT USAGE, SELECT ON SEQUENCE public.interview_collection_roles_id_seq TO authenticated;

CREATE POLICY interview_collection_roles_select
  ON public.interview_collection_roles
  FOR SELECT
  TO PUBLIC
  USING (true);

CREATE POLICY interview_collection_roles_insert
  ON public.interview_collection_roles
  FOR INSERT
  TO PUBLIC
  WITH CHECK (true);

CREATE POLICY interview_collection_roles_update
  ON public.interview_collection_roles
  FOR UPDATE
  TO PUBLIC
  USING (true)
  WITH CHECK (true);

CREATE POLICY interview_collection_roles_delete
  ON public.interview_collection_roles
  FOR DELETE
  TO PUBLIC
  USING (true);

INSERT INTO public.interview_collection_roles (collection_id, role_slug)
SELECT DISTINCT links.collection_id, 'programmer'
FROM public.interview_collection_tags AS links
JOIN public.interview_tags AS tags ON tags.id = links.tag_id
WHERE tags.slug = 'hot'
ON CONFLICT (role_slug, collection_id) DO NOTHING;
