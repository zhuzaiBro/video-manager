package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/zood/video-manager/internal/config"
	"github.com/zood/video-manager/internal/model"
	"github.com/zood/video-manager/internal/queue"
	"github.com/zood/video-manager/internal/repository"
	"gorm.io/gorm"
)

var (
	ErrVideoNotFound      = errors.New("video not found")
	ErrVideoNotReady      = errors.New("video not ready")
	ErrWatchLimitExceeded = errors.New("daily watch limit exceeded")
)

type VideoService struct {
	cfg    *config.Config
	videos *repository.VideoRepository
	queue  *queue.Client
}

func NewVideoService(cfg *config.Config, videos *repository.VideoRepository, q *queue.Client) *VideoService {
	return &VideoService{cfg: cfg, videos: videos, queue: q}
}

func (s *VideoService) Upload(ctx context.Context, title string, description *string, sourcePath string) (uuid.UUID, error) {
	video := &model.Video{
		Title:       title,
		Description: description,
		SourceFile:  sourcePath,
		Status:      model.VideoStatusWaiting,
	}
	if err := s.videos.Create(video); err != nil {
		return uuid.Nil, err
	}

	if err := s.queue.EnqueueTranscode(video.ID); err != nil {
		_ = s.videos.UpdateStatus(video.ID, model.VideoStatusFailed, strPtr("failed to enqueue transcode task"))
		return uuid.Nil, fmt.Errorf("enqueue transcode: %w", err)
	}

	return video.ID, nil
}

func (s *VideoService) GetByID(id uuid.UUID) (*model.Video, error) {
	video, err := s.videos.FindByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrVideoNotFound
		}
		return nil, err
	}
	return video, nil
}

func (s *VideoService) List(page, pageSize int) ([]model.Video, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize
	return s.videos.List(offset, pageSize)
}

func strPtr(s string) *string {
	return &s
}

func EnsureDirs(dirs ...string) error {
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("create dir %s: %w", d, err)
		}
	}
	return nil
}

func SaveUploadFile(uploadDir, filename string, data []byte) (string, error) {
	path := filepath.Join(uploadDir, filename)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}
