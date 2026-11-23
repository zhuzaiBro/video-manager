-- Remote Supabase project gmfuorleragagsqbkajh only.
-- Not applied by this change. Do not run it until the launch prerequisites
-- in the pull request are accepted.
--
-- Business messages stay in the local database. This script does not create
-- a messages table, does not add anything to supabase_realtime, and does not
-- alter the locked realtime schema. The 2026-07-14 Realtime change still
-- allows policies on realtime.messages.
--
-- A browser may subscribe only when its short-lived JWT app_metadata.support_topic
-- equals the private topic. The topic is an HMAC, not a user or session UUID.
-- Realtime reads that claim from request.jwt.claims. Do not authorize with
-- user_metadata. It is editable by the user.
-- There is no INSERT policy: the browser cannot publish. The Go API publishes
-- with the server-side service role after the local message commit.
-- Before relying on this policy, disable Realtime "Allow public access" in the
-- project settings. This script does not change that setting.
-- Do not add ALTER TABLE realtime.messages. RLS is already enabled, and the
-- statement fails because the realtime schema is locked.

DROP POLICY IF EXISTS "support private broadcast receive" ON realtime.messages;

CREATE POLICY "support private broadcast receive"
ON realtime.messages
FOR SELECT
TO anon
USING (
  realtime.messages.extension = 'broadcast'
  AND (SELECT realtime.topic()) LIKE 'support.v1.%'
  AND (SELECT realtime.topic()) = coalesce(
    nullif(current_setting('request.jwt.claims', true), '')::json #>> '{app_metadata,support_topic}',
    ''
  )
);
