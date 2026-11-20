package main

import (
	"log"

	"github.com/gin-gonic/gin"
	"github.com/zood/video-manager/internal/auth"
	"github.com/zood/video-manager/internal/config"
	"github.com/zood/video-manager/internal/platform"
	platformpg "github.com/zood/video-manager/internal/platform/postgres"
	platformsb "github.com/zood/video-manager/internal/platform/supabase"
)

func registerPlatform(r *gin.Engine, cfg *config.Config, validator *auth.Validator) {
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
	platform.NewHandler(platform.NewService(source, storage, dataSource), validator).Register(r)
	log.Printf("platform API enabled, data_source=%s", dataSource)
}
