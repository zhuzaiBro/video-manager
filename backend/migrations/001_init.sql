CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE IF NOT EXISTS videos (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title         TEXT NOT NULL,
    description   TEXT,
    source_file   TEXT NOT NULL,
    cos_prefix    TEXT,
    m3u8_path     TEXT,
    duration      INTEGER,
    width         INTEGER,
    height        INTEGER,
    fps           NUMERIC(10, 2),
    segment_count INTEGER,
    status        TEXT NOT NULL DEFAULT 'waiting',
    error_message TEXT,
    created_at    TIMESTAMPTZ DEFAULT NOW(),
    updated_at    TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS user_video_usage (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id       TEXT NOT NULL,
    usage_date    DATE NOT NULL,
    watch_seconds INTEGER NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ DEFAULT NOW(),
    updated_at    TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE (user_id, usage_date)
);

CREATE TABLE IF NOT EXISTS video_access_logs (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id       TEXT NOT NULL,
    video_id      UUID NOT NULL REFERENCES videos(id),
    segment_name  TEXT,
    watch_seconds INTEGER,
    ip            INET,
    created_at    TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_videos_status ON videos (status);
CREATE INDEX IF NOT EXISTS idx_user_video_usage_user_date ON user_video_usage (user_id, usage_date);
CREATE INDEX IF NOT EXISTS idx_video_access_logs_user_video ON video_access_logs (user_id, video_id);
