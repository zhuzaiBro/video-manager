-- Local password lookup and the few writes that row policies cannot express.
-- Apply as the table owner, not as a superuser. Do not replace auth.uid().
-- anon and authenticated are not granted SELECT on auth.users.

CREATE SCHEMA IF NOT EXISTS platform;

CREATE OR REPLACE FUNCTION platform.password_hash_for_email(p_email text)
RETURNS TABLE(id uuid, email text, encrypted_password text, created_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = auth, pg_temp
AS $$
  SELECT users.id, users.email::text, users.encrypted_password, users.created_at
  FROM auth.users AS users
  WHERE lower(users.email::text) = lower(p_email)
  LIMIT 1
$$;

CREATE OR REPLACE FUNCTION platform.register_password_user(p_id uuid, p_email text, p_hash text)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = auth, public, pg_temp
AS $$
BEGIN
  IF p_hash IS NULL OR left(p_hash, 2) <> '$2' THEN
    RAISE EXCEPTION 'password hash must be bcrypt' USING ERRCODE = '22023';
  END IF;
  INSERT INTO auth.users (id, email, encrypted_password, email_confirmed_at, created_at, updated_at)
  VALUES (p_id, lower(p_email), p_hash, NULL, now(), now());
  INSERT INTO public.user_profiles (id, username, display_name, is_admin)
  VALUES (p_id, split_part(lower(p_email), '@', 1), split_part(lower(p_email), '@', 1), false)
  ON CONFLICT (id) DO NOTHING;
END;
$$;

CREATE OR REPLACE FUNCTION platform.own_auth_account()
RETURNS TABLE(id uuid, email text, phone text, created_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = auth, pg_temp
AS $$
  SELECT users.id, users.email::text, COALESCE(users.phone::text, ''), users.created_at
  FROM auth.users AS users WHERE users.id = auth.uid()
$$;

CREATE OR REPLACE FUNCTION platform.assert_admin()
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, auth, pg_temp
AS $$
BEGIN
  IF auth.uid() IS NULL OR NOT EXISTS (
    SELECT 1 FROM public.user_profiles WHERE id = auth.uid() AND is_admin IS TRUE
  ) THEN
    RAISE EXCEPTION 'admin required' USING ERRCODE = '42501';
  END IF;
END;
$$;

CREATE OR REPLACE FUNCTION platform.admin_read_profile(p_user uuid)
RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, auth, pg_temp
AS $$
DECLARE row_data jsonb;
BEGIN
  PERFORM platform.assert_admin();
  SELECT to_jsonb(profiles) INTO row_data FROM public.user_profiles AS profiles WHERE profiles.id = p_user;
  RETURN row_data;
END;
$$;

CREATE OR REPLACE FUNCTION platform.admin_set_vip(p_user uuid, p_level integer, p_expires timestamptz)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, auth, pg_temp
AS $$
DECLARE rewarded boolean;
BEGIN
  IF auth.uid() IS NULL THEN
    RAISE EXCEPTION 'admin required' USING ERRCODE = '42501';
  END IF;
  IF EXISTS (SELECT 1 FROM public.user_profiles WHERE id = auth.uid() AND is_admin IS TRUE) THEN
    UPDATE public.user_profiles SET vip_level = p_level, vip_expires_at = p_expires WHERE id = p_user;
    IF NOT FOUND THEN
      RAISE EXCEPTION 'profile not found' USING ERRCODE = 'P0002';
    END IF;
    RETURN;
  END IF;
  SELECT EXISTS (
    SELECT 1 FROM public.interview_referral_rewards
    WHERE referrer_user_id = p_user AND referred_user_id = auth.uid()
  ) INTO rewarded;
  IF NOT rewarded THEN
    RAISE EXCEPTION 'admin required' USING ERRCODE = '42501';
  END IF;
  UPDATE public.user_profiles
  SET vip_level = GREATEST(vip_level, 1),
      vip_expires_at = GREATEST(COALESCE(vip_expires_at, now()), now()) + interval '1 day'
  WHERE id = p_user;
END;
$$;

CREATE OR REPLACE FUNCTION platform.referral_exists(p_referred uuid)
RETURNS boolean LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path = public, auth, pg_temp
AS $$
BEGIN
  IF auth.uid() IS DISTINCT FROM p_referred AND NOT EXISTS (
    SELECT 1 FROM public.user_profiles WHERE id = auth.uid() AND is_admin IS TRUE
  ) THEN
    RAISE EXCEPTION 'admin required' USING ERRCODE = '42501';
  END IF;
  RETURN EXISTS (SELECT 1 FROM public.interview_referral_rewards WHERE referred_user_id = p_referred);
END;
$$;

CREATE OR REPLACE FUNCTION platform.insert_referral(
  p_referrer uuid, p_referred uuid, p_collection uuid, p_url text, p_days integer, p_level integer
) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, auth, pg_temp
AS $$
BEGIN
  IF auth.uid() IS DISTINCT FROM p_referred THEN
    PERFORM platform.assert_admin();
  END IF;
  INSERT INTO public.interview_referral_rewards (
    referrer_user_id, referred_user_id, source_collection_id, source_url, reward_days, reward_vip_level
  ) VALUES (p_referrer, p_referred, p_collection, p_url, p_days, p_level);
END;
$$;

CREATE OR REPLACE FUNCTION platform.admin_list_auth_users()
RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path = auth, public, pg_temp
AS $$
BEGIN
  PERFORM platform.assert_admin();
  RETURN COALESCE((
    SELECT jsonb_agg(jsonb_build_object(
      'id', users.id, 'email', users.email::text,
      'phone', COALESCE(users.phone::text, ''), 'created_at', users.created_at
    )) FROM auth.users AS users
  ), '[]'::jsonb);
END;
$$;

CREATE OR REPLACE FUNCTION platform.admin_get_auth_user(p_user uuid)
RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path = auth, public, pg_temp
AS $$
DECLARE row_data jsonb;
BEGIN
  PERFORM platform.assert_admin();
  SELECT jsonb_build_object(
    'id', users.id, 'email', users.email::text,
    'phone', COALESCE(users.phone::text, ''), 'created_at', users.created_at
  ) INTO row_data FROM auth.users AS users WHERE users.id = p_user;
  RETURN row_data;
END;
$$;

CREATE OR REPLACE FUNCTION platform.upsert_own_or_admin_profile(p_id uuid, p_username text, p_display text, p_avatar text)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, auth, pg_temp
AS $$
BEGIN
  IF auth.uid() IS DISTINCT FROM p_id THEN
    PERFORM platform.assert_admin();
  END IF;
  INSERT INTO public.user_profiles (id, username, display_name, avatar_url, is_admin)
  VALUES (p_id, p_username, p_display, p_avatar, false)
  ON CONFLICT (id) DO UPDATE
  SET username = COALESCE(EXCLUDED.username, public.user_profiles.username),
      display_name = COALESCE(EXCLUDED.display_name, public.user_profiles.display_name),
      avatar_url = COALESCE(EXCLUDED.avatar_url, public.user_profiles.avatar_url);
END;
$$;

CREATE OR REPLACE FUNCTION platform.admin_list_profiles()
RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, auth, pg_temp
AS $$
BEGIN
  PERFORM platform.assert_admin();
  RETURN COALESCE((SELECT jsonb_agg(to_jsonb(profiles)) FROM public.user_profiles AS profiles), '[]'::jsonb);
END;
$$;

CREATE OR REPLACE FUNCTION platform.admin_watch_logs(p_since timestamptz)
RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, auth, pg_temp
AS $$
BEGIN
  PERFORM platform.assert_admin();
  RETURN COALESCE((
    SELECT jsonb_agg(jsonb_build_object(
      'user_id', logs.user_id, 'video_id', logs.video_id,
      'watch_seconds', logs.watch_seconds, 'created_at', logs.created_at
    )) FROM public.video_access_logs AS logs WHERE logs.created_at >= p_since
  ), '[]'::jsonb);
END;
$$;

CREATE OR REPLACE FUNCTION platform.admin_mark_consultation_paid(p_id integer, p_user uuid, p_paid_at timestamptz)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, auth, pg_temp
AS $$
BEGIN
  PERFORM platform.assert_admin();
  UPDATE public.membership_consultation_requests
  SET status = 'paid', paid_at = p_paid_at, user_id = p_user
  WHERE id = p_id;
END;
$$;

REVOKE ALL ON FUNCTION platform.password_hash_for_email(text) FROM PUBLIC;
REVOKE ALL ON FUNCTION platform.register_password_user(uuid, text, text) FROM PUBLIC;
REVOKE ALL ON FUNCTION platform.own_auth_account() FROM PUBLIC;
REVOKE ALL ON FUNCTION platform.assert_admin() FROM PUBLIC;
REVOKE ALL ON FUNCTION platform.admin_read_profile(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION platform.admin_set_vip(uuid, integer, timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION platform.referral_exists(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION platform.insert_referral(uuid, uuid, uuid, text, integer, integer) FROM PUBLIC;
REVOKE ALL ON FUNCTION platform.admin_list_auth_users() FROM PUBLIC;
REVOKE ALL ON FUNCTION platform.admin_get_auth_user(uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION platform.upsert_own_or_admin_profile(uuid, text, text, text) FROM PUBLIC;
REVOKE ALL ON FUNCTION platform.admin_list_profiles() FROM PUBLIC;
REVOKE ALL ON FUNCTION platform.admin_watch_logs(timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION platform.admin_mark_consultation_paid(integer, uuid, timestamptz) FROM PUBLIC;

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'zood_app') THEN
    GRANT USAGE ON SCHEMA platform TO zood_app;
    GRANT EXECUTE ON FUNCTION platform.password_hash_for_email(text) TO zood_app;
    GRANT EXECUTE ON FUNCTION platform.register_password_user(uuid, text, text) TO zood_app;
  END IF;
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'authenticated') THEN
    GRANT USAGE ON SCHEMA platform TO authenticated;
    GRANT EXECUTE ON FUNCTION platform.own_auth_account() TO authenticated;
    GRANT EXECUTE ON FUNCTION platform.admin_read_profile(uuid) TO authenticated;
    GRANT EXECUTE ON FUNCTION platform.admin_set_vip(uuid, integer, timestamptz) TO authenticated;
    GRANT EXECUTE ON FUNCTION platform.referral_exists(uuid) TO authenticated;
    GRANT EXECUTE ON FUNCTION platform.insert_referral(uuid, uuid, uuid, text, integer, integer) TO authenticated;
    GRANT EXECUTE ON FUNCTION platform.admin_list_auth_users() TO authenticated;
    GRANT EXECUTE ON FUNCTION platform.admin_get_auth_user(uuid) TO authenticated;
    GRANT EXECUTE ON FUNCTION platform.upsert_own_or_admin_profile(uuid, text, text, text) TO authenticated;
    GRANT EXECUTE ON FUNCTION platform.admin_list_profiles() TO authenticated;
    GRANT EXECUTE ON FUNCTION platform.admin_watch_logs(timestamptz) TO authenticated;
    GRANT EXECUTE ON FUNCTION platform.admin_mark_consultation_paid(integer, uuid, timestamptz) TO authenticated;
  END IF;
END $$;

CREATE OR REPLACE FUNCTION platform.list_telegram_admins()
RETURNS jsonb LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp
AS $$ SELECT COALESCE((SELECT jsonb_agg(chat_id) FROM public.telegram_support_admins WHERE active IS TRUE), '[]'::jsonb) $$;
CREATE OR REPLACE FUNCTION platform.upsert_telegram_admin(p_chat bigint, p_user bigint, p_name text)
RETURNS void LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp
AS $$ INSERT INTO public.telegram_support_admins (chat_id, telegram_user_id, username, active, updated_at)
VALUES (p_chat, p_user, p_name, true, now())
ON CONFLICT (chat_id) DO UPDATE SET telegram_user_id = EXCLUDED.telegram_user_id, username = EXCLUDED.username, active = true, updated_at = now() $$;
CREATE OR REPLACE FUNCTION platform.telegram_admin_active(p_chat bigint, p_user bigint)
RETURNS boolean LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp
AS $$ SELECT EXISTS (SELECT 1 FROM public.telegram_support_admins WHERE chat_id = p_chat AND telegram_user_id = p_user AND active IS TRUE) $$;
CREATE OR REPLACE FUNCTION platform.telegram_update_seen(p_update bigint)
RETURNS boolean LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp
AS $$ SELECT EXISTS (SELECT 1 FROM public.telegram_message_links WHERE telegram_update_id = p_update) $$;
CREATE OR REPLACE FUNCTION platform.telegram_reply_session(p_chat bigint, p_message bigint)
RETURNS text LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp
AS $$ SELECT session_id::text FROM public.telegram_message_links WHERE telegram_chat_id = p_chat AND telegram_message_id = p_message AND direction = 'to_telegram' LIMIT 1 $$;
CREATE OR REPLACE FUNCTION platform.insert_admin_message(p_session uuid, p_content text)
RETURNS uuid LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp
AS $$ DECLARE new_id uuid; BEGIN
  INSERT INTO public.messages (session_id, sender_type, content) VALUES (p_session, 'admin', p_content) RETURNING id INTO new_id;
  RETURN new_id; END $$;
CREATE OR REPLACE FUNCTION platform.insert_telegram_link(p_row jsonb)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp
AS $$ BEGIN INSERT INTO public.telegram_message_links (session_id, telegram_chat_id, telegram_message_id, telegram_update_id, direction)
VALUES (NULLIF(p_row->>'session_id','')::uuid, NULLIF(p_row->>'telegram_chat_id','')::bigint, NULLIF(p_row->>'telegram_message_id','')::bigint, NULLIF(p_row->>'telegram_update_id','')::bigint, p_row->>'direction'); END $$;
REVOKE ALL ON FUNCTION platform.list_telegram_admins() FROM PUBLIC;
REVOKE ALL ON FUNCTION platform.upsert_telegram_admin(bigint, bigint, text) FROM PUBLIC;
REVOKE ALL ON FUNCTION platform.telegram_admin_active(bigint, bigint) FROM PUBLIC;
REVOKE ALL ON FUNCTION platform.telegram_update_seen(bigint) FROM PUBLIC;
REVOKE ALL ON FUNCTION platform.telegram_reply_session(bigint, bigint) FROM PUBLIC;
REVOKE ALL ON FUNCTION platform.insert_admin_message(uuid, text) FROM PUBLIC;
REVOKE ALL ON FUNCTION platform.insert_telegram_link(jsonb) FROM PUBLIC;
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'zood_app') THEN
    GRANT EXECUTE ON FUNCTION platform.list_telegram_admins() TO zood_app;
    GRANT EXECUTE ON FUNCTION platform.upsert_telegram_admin(bigint, bigint, text) TO zood_app;
    GRANT EXECUTE ON FUNCTION platform.telegram_admin_active(bigint, bigint) TO zood_app;
    GRANT EXECUTE ON FUNCTION platform.telegram_update_seen(bigint) TO zood_app;
    GRANT EXECUTE ON FUNCTION platform.telegram_reply_session(bigint, bigint) TO zood_app;
    GRANT EXECUTE ON FUNCTION platform.insert_admin_message(uuid, text) TO zood_app;
    GRANT EXECUTE ON FUNCTION platform.insert_telegram_link(jsonb) TO zood_app;
  END IF;
END $$;
