package service

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/zood/video-manager/internal/config"
	cosclient "github.com/zood/video-manager/internal/cos"
	"github.com/zood/video-manager/internal/ffmpeg"
	"github.com/zood/video-manager/internal/ffprobe"
	"github.com/zood/video-manager/internal/model"
	"github.com/zood/video-manager/internal/queue"
	"github.com/zood/video-manager/internal/repository"
)

type TranscodeService struct {
	cfg    *config.Config
	videos *repository.VideoRepository
	cos    *cosclient.Client
}

func NewTranscodeService(cfg *config.Config, videos *repository.VideoRepository, cos *cosclient.Client) *TranscodeService {
	return &TranscodeService{cfg: cfg, videos: videos, cos: cos}
}

func (s *TranscodeService) HandleTranscode(ctx context.Context, payload queue.TranscodePayload) error {
	video, err := s.videos.FindByID(payload.VideoID)
	if err != nil {
		return fmt.Errorf("find video: %w", err)
	}

	if err := s.videos.UpdateStatus(video.ID, model.VideoStatusProcessing, nil); err != nil {
		return err
	}

	probe, err := ffprobe.Probe(s.cfg.FFprobePath, video.SourceFile)
	if err != nil {
		return s.fail(video.ID, err)
	}

	outputDir := filepath.Join(s.cfg.TempDir, fmt.Sprintf("hls_%s", video.ID))
	defer os.RemoveAll(outputDir)

	result, err := ffmpeg.TranscodeHLS(s.cfg.FFmpegPath, video.SourceFile, outputDir)
	if err != nil {
		return s.fail(video.ID, err)
	}

	cosPrefix := s.cos.Prefix(video.ID)
	if err := s.cos.UploadDir(ctx, outputDir, cosPrefix); err != nil {
		return s.fail(video.ID, err)
	}

	m3u8Path := fmt.Sprintf("videos/%s/index.m3u8", video.ID)
	duration := int(math.Round(probe.Duration))
	fps := probe.FPS

	updates := map[string]interface{}{
		"cos_prefix":    cosPrefix,
		"m3u8_path":     m3u8Path,
		"duration":      duration,
		"width":         probe.Width,
		"height":        probe.Height,
		"fps":           fps,
		"segment_count": result.SegmentCount,
		"status":        model.VideoStatusReady,
		"error_message": nil,
	}

	if err := s.videos.UpdateAfterTranscode(video.ID, updates); err != nil {
		return err
	}

	return nil
}

func (s *TranscodeService) fail(videoID uuid.UUID, err error) error {
	msg := err.Error()
	_ = s.videos.UpdateStatus(videoID, model.VideoStatusFailed, &msg)
	return err
}
