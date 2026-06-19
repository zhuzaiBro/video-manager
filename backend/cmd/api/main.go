package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zood/video-manager/internal/auth"
	cosclient "github.com/zood/video-manager/internal/cos"
	"github.com/zood/video-manager/internal/config"
	"github.com/zood/video-manager/internal/handler"
	"github.com/zood/video-manager/internal/middleware"
	"github.com/zood/video-manager/internal/queue"
	redisclient "github.com/zood/video-manager/internal/redis"
	"github.com/zood/video-manager/internal/repository"
	"github.com/zood/video-manager/internal/service"
)

func main() {
	cfg := config.Load()

	if err := service.EnsureDirs(cfg.UploadDir, cfg.TempDir); err != nil {
		log.Fatalf("ensure dirs: %v", err)
	}

	db, err := repository.NewDB(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}

	cos, err := cosclient.NewClient(cfg)
	if err != nil {
		log.Fatalf("cos client: %v", err)
	}

	redis := redisclient.NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := redis.Ping(ctx); err != nil {
		log.Printf("warning: redis ping failed: %v", err)
	}
	cancel()

	q := queue.NewClient(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	defer q.Close()

	validator := auth.NewValidator(cfg)
	authMiddleware := middleware.NewAuthMiddleware(validator)

	videoRepo := repository.NewVideoRepository(db)
	usageRepo := repository.NewUsageRepository(db)

	videoSvc := service.NewVideoService(cfg, videoRepo, q)
	playbackSvc := service.NewPlaybackService(cfg, videoRepo, usageRepo, redis, cos)
	authSvc := service.NewAuthService(cfg, validator)

	adminHandler := handler.NewAdminHandler(cfg, videoSvc)
	videoHandler := handler.NewVideoHandler(videoSvc, playbackSvc)
	authHandler := handler.NewAuthHandler(authSvc)

	r := gin.Default()
	r.Use(middleware.CORS(cfg.CORSOrigins))
	r.GET("/health", handler.Health)

	api := r.Group("/api")
	{
		api.POST("/auth/tencent-token", authHandler.ExchangeTencentToken)

		videos := api.Group("/videos")
		{
			videos.GET("", videoHandler.List)
			videos.GET("/:id", videoHandler.Detail)
			videos.GET("/:id/play", authMiddleware.RequireAuthFromQuery(), videoHandler.Play)
			videos.GET("/:id/media/*filepath", authMiddleware.RequireAuthFromQuery(), videoHandler.Media)

			authGroup := videos.Group("", authMiddleware.RequireAuth())
			authGroup.POST("/:id/segments", videoHandler.ReportSegment)
		}

		admin := api.Group("/admin")
		{
			admin.POST("/videos/upload", adminHandler.Upload)
			admin.GET("/videos/:id/watch-stats", videoHandler.WatchStats)
		}
	}

	srv := &http.Server{
		Addr:    ":" + cfg.ServerPort,
		Handler: r,
	}

	go func() {
		log.Printf("api listening on :%s | cos=%s | api_base=%s",
			cfg.ServerPort, cfg.COSCustomDomain, cfg.APIBaseURL)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("shutdown: %v", err)
	}
}
