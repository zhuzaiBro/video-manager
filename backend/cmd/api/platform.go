package main

import (
	"context"
	"log"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/zood/video-manager/internal/auth"
	"github.com/zood/video-manager/internal/config"
	"github.com/zood/video-manager/internal/platform"
	platformpg "github.com/zood/video-manager/internal/platform/postgres"
	platformsb "github.com/zood/video-manager/internal/platform/supabase"
	"github.com/zood/video-manager/internal/repository"
)

func registerPlatform(r *gin.Engine, cfg *config.Config, validator *auth.Validator, videos *repository.VideoRepository) {
	var source platform.Source
	var storage platform.ObjectStorage
	dataSource := cfg.PlatformDataSource
	switch dataSource {
	case "postgres":
		if cfg.PlatformDatabaseURL == "" {
			log.Println("warning: platform API disabled, PLATFORM_DATABASE_URL is empty")
			return
		}
		pg, err := platformpg.Open(cfg.PlatformDatabaseURL)
		if err != nil {
			log.Printf("warning: platform postgres unavailable, API disabled: %v", err)
			return
		}
		source = pg
	default:
		dataSource = "supabase"
		client, err := platformsb.New(cfg.SupabaseURL, cfg.SupabaseAnonKey)
		if err != nil {
			log.Printf("warning: platform API disabled: %v", err)
			return
		}
		source = client
		storage = client
	}
	service := platform.NewService(source, storage, dataSource)
	if cfg.SupabaseServiceRoleKey != "" {
		admin, err := platformsb.NewAdmin(cfg.SupabaseURL, cfg.SupabaseServiceRoleKey)
		if err != nil {
			log.Printf("warning: isolated admin client disabled: %v", err)
		} else {
			service.SetPrivileged(admin)
			log.Print("platform isolated admin client enabled")
		}
	} else {
		log.Print("warning: SUPABASE_SERVICE_ROLE_KEY is empty; referral, membership, profile sync and telegram mapping return 503")
	}
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
	platform.NewHandler(service, validator).Register(r)
	log.Printf("platform API enabled, data_source=%s", dataSource)
}
