package main

import (
	"context"
	"github.com/joho/godotenv"
	"github.com/zood/video-manager/internal/support"
	"log"
	"net/url"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/zood/video-manager/internal/auth"
	"github.com/zood/video-manager/internal/config"
	"github.com/zood/video-manager/internal/gormplus"
	"github.com/zood/video-manager/internal/platform"
	"github.com/zood/video-manager/internal/platform/content"
	platformpg "github.com/zood/video-manager/internal/platform/postgres"
	redisclient "github.com/zood/video-manager/internal/redis"
	"github.com/zood/video-manager/internal/repository"
)

func registerPlatform(lifetime context.Context, r *gin.Engine, cfg *config.Config, validator *auth.Validator, videos *repository.VideoRepository, blob platform.BlobStore, cache *redisclient.Client) {
	if cfg.PlatformDataSource != "postgres" {
		log.Println("warning: platform API disabled, Supabase is not used; set PLATFORM_DATA_SOURCE=postgres")
		return
	}
	if cfg.PlatformDatabaseURL == "" {
		log.Println("warning: platform API disabled, PLATFORM_DATABASE_URL is empty")
		return
	}
	if sameDatabase(cfg.DatabaseURL, cfg.PlatformDatabaseURL) {
		log.Println("warning: platform API disabled, PLATFORM_DATABASE_URL must not be the video DATABASE_URL")
		return
	}
	pg, err := platformpg.Open(cfg.PlatformDatabaseURL)
	if err != nil {
		log.Printf("warning: platform postgres unavailable, API disabled: %v", err)
		return
	}
	const dataSource = "postgres"
	service := platform.NewService(pg, nil, dataSource)
	posts, err := gormplus.Use(pg.Pool(), gormplus.Platform)
	if err != nil {
		log.Printf("warning: platform postgres unavailable, API disabled: %v", err)
		return
	}
	catalog, err := content.New(posts)
	if err != nil {
		log.Printf("warning: platform postgres unavailable, API disabled: %v", err)
		return
	}
	service.SetCatalog(catalog)
	_ = godotenv.Load("/etc/zood/support.env")
	if dsn := os.Getenv("SUPPORT_DATABASE_URL"); dsn != "" {
		inbox, err := support.Open(dsn, os.Getenv("SUPPORT_ENCRYPTION_KEY"), cfg.APIBaseURL, cfg.SupabaseURL, os.Getenv("SUPABASE_PUBLISHABLE_KEY"), os.Getenv("SUPPORT_TELEGRAM_SIGNING_SEED"), cache)
		if err != nil {
			log.Printf("support service unavailable: %v", err)
		} else {
			service.SetSupport(inbox)
			go inbox.Run(lifetime)
		}
	}
	service.SetPrivileged(pg)
	service.SetTelegramSecrets(cfg.TelegramWebhookSecret, cfg.TelegramSetupSecret)
	if cfg.TelegramBotToken != "" {
		service.SetTelegram(platform.NewTelegram(cfg.TelegramBotToken))
	}
	if videos != nil {
		service.SetDurationLookup(func(_ context.Context, id string) int {
			parsed, err := uuid.Parse(id)
			if err != nil {
				return 0
			}
			video, err := videos.FindByID(parsed)
			if err != nil || video == nil || video.Duration == nil {
				return 0
			}
			return *video.Duration
		})
	}
	handler := platform.NewHandler(service, validator)
	handler.SetBlob(blob)
	handler.Register(r)
	log.Printf("platform API enabled, data_source=%s", dataSource)
}

func sameDatabase(left, right string) bool {
	return normalizeDatabase(left) != "" && normalizeDatabase(left) == normalizeDatabase(right)
}

func normalizeDatabase(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return strings.TrimSpace(raw)
	}
	parsed.User = nil
	return parsed.Scheme + "://" + parsed.Host + parsed.Path
}
