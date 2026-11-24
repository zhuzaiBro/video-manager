BEGIN;
SET LOCAL lock_timeout='4s';
DO $$
BEGIN
  IF to_regclass('support.message_reads') IS NULL THEN
    CREATE TABLE support.message_reads (
      message_id uuid PRIMARY KEY REFERENCES public.messages(id) ON DELETE CASCADE,
      read_at timestamptz
    );
    -- Old replies predate unread badges. Keep them out of the new-message count
    -- without inventing a time at which the customer read them.
    INSERT INTO support.message_reads(message_id)
      SELECT id FROM public.messages WHERE sender_type='admin';
  END IF;
END $$;
REVOKE ALL ON support.message_reads FROM PUBLIC, anon, authenticated;
GRANT SELECT, INSERT ON support.message_reads TO zood_support_api;
COMMIT;
