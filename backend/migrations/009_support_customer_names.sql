-- The support inbox and Telegram delivery read the same public profile names as the site.
GRANT SELECT (display_name, username) ON public.user_profiles TO zood_support_api;
