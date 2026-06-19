package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/hibiken/asynq"
	cosclient "github.com/zood/video-manager/internal/cos"
	"github.com/zood/video-manager/internal/config"
	"github.com/zood/video-manager/internal/queue"
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

	videoRepo := repository.NewVideoRepository(db)
	transcodeSvc := service.NewTranscodeService(cfg, videoRepo, cos)

	srv := queue.NewServer(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	mux := queue.NewServeMux()

	mux.HandleFunc(queue.TaskVideoTranscode, func(ctx context.Context, t *asynq.Task) error {
		var payload queue.TranscodePayload
		if err := json.Unmarshal(t.Payload(), &payload); err != nil {
			return err
		}
		log.Printf("processing transcode task for video %d", payload.VideoID)
		return transcodeSvc.HandleTranscode(ctx, payload)
	})

	go func() {
		log.Println("video-worker started")
		if err := srv.Run(mux); err != nil {
			log.Fatalf("worker run: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	srv.Shutdown()
	log.Println("video-worker stopped")
}
