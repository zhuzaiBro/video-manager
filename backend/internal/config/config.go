package config

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const (
	DailyWatchLimitSeconds = 28800
	SegmentDurationSeconds = 6
	UsageKeyTTL            = 48 * time.Hour
	OnlineDeviceTTL        = 60 * time.Second

	DefaultChunkSize  = 5 * 1024 * 1024 // 5MB
	MaxUploadFileSize = 20 * 1024 * 1024 * 1024 // 20GB
	ChunkUploadTTL    = 24 * time.Hour
)

type Config struct {
	ServerPort string

	DatabaseURL string

	RedisAddr     string
	RedisPassword string
	RedisDB       int

	UploadDir   string
	TempDir     string
	FFmpegPath  string
	FFprobePath string

	ChunkSize         int64
	MaxUploadFileSize int64
	ChunkUploadTTL    time.Duration

	COSSecretID  string
	COSSecretKey string
	COSBucket         string
	COSRegion         string
	COSCustomDomain   string // COS 控制台绑定的自定义域名，如 https://workspace.zood.work

	// API 对外地址（play/m3u8 入口，可与 COS 域名不同）
	APIBaseURL string
	// 兼容旧配置
	ProxyBaseURL string

	// 保留兼容，不再用于播放
	CDNDomain    string
	CDNSignKey   string
	CDNExpireSec int

	CORSOrigins       string
	JWTSecret         string
	SupabaseJWTSecret string
	TokenExpireSec    int
}

func Load() *Config {
	loadEnvFile()

	cfg := &Config{
		ServerPort: getEnv("SERVER_PORT", "8080"),

		DatabaseURL: getEnv("DATABASE_URL", "postgres://video:video@localhost:5432/video_manager?sslmode=disable"),

		RedisAddr:     getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),
		RedisDB:       getEnvInt("REDIS_DB", 0),

		UploadDir:   getEnv("UPLOAD_DIR", "./data/uploads"),
		TempDir:     getEnv("TEMP_DIR", "./data/temp"),
		FFmpegPath:  getEnv("FFMPEG_PATH", "ffmpeg"),
		FFprobePath: getEnv("FFPROBE_PATH", "ffprobe"),

		ChunkSize:         getEnvInt64("CHUNK_SIZE", DefaultChunkSize),
		MaxUploadFileSize: getEnvInt64("MAX_UPLOAD_FILE_SIZE", MaxUploadFileSize),
		ChunkUploadTTL:    time.Duration(getEnvInt("CHUNK_UPLOAD_TTL_HOURS", 24)) * time.Hour,

		COSSecretID:  getEnv("COS_SECRET_ID", ""),
		COSSecretKey: getEnv("COS_SECRET_KEY", ""),
		COSBucket:       getEnv("COS_BUCKET", ""),
		COSRegion:       getEnv("COS_REGION", "ap-guangzhou"),
		COSCustomDomain: getEnv("COS_CUSTOM_DOMAIN", ""),

		APIBaseURL:   getEnv("API_BASE_URL", getEnv("PROXY_BASE_URL", "http://localhost:7778")),
		ProxyBaseURL: getEnv("PROXY_BASE_URL", "http://localhost:7778"),

		CDNDomain:    getEnv("CDN_DOMAIN", ""),
		CDNSignKey:   getEnv("CDN_SIGN_KEY", ""),
		CDNExpireSec: getEnvInt("CDN_EXPIRE_SEC", 3600),

		JWTSecret:         getEnv("JWT_SECRET", "dev-secret-change-me"),
		CORSOrigins:       getEnv("CORS_ORIGINS", "*"),
		SupabaseJWTSecret: getEnv("SUPABASE_JWT_SECRET", ""),
		TokenExpireSec:    getEnvInt("TOKEN_EXPIRE_SEC", 3600),
	}

	if cfg.SupabaseJWTSecret == "" {
		log.Println("warning: SUPABASE_JWT_SECRET is empty, /api/auth/tencent-token will fail")
	}

	return cfg
}

func loadEnvFile() {
	candidates := []string{".env", filepath.Join("backend", ".env")}
	for _, p := range candidates {
		if err := godotenv.Load(p); err == nil {
			return
		}
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return trimQuotes(v)
	}
	return fallback
}

func trimQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvInt64(key string, fallback int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return fallback
}
