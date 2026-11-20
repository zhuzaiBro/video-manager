package main

import (
	"context"
	"log"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/zood/video-manager/internal/auth"
	"github.com/zood/video-manager/internal/config"
	"github.com/zood/video-manager/internal/platform"
	platformpg "github.com/zood/video-manager/internal/platform/postgres"
	"github.com/zood/video-manager/internal/repository"
)

func registerPlatform(r *gin.Engine, cfg *config.Config, validator *auth.Validator, videos *repository.VideoRepository, blob platform.BlobStore) {
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
