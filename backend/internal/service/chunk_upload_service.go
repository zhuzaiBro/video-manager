package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zood/video-manager/internal/config"
)

var (
	ErrUploadSessionNotFound = errors.New("upload session not found")
	ErrUploadSessionExpired  = errors.New("upload session expired")
	ErrInvalidChunkIndex     = errors.New("invalid chunk index")
	ErrInvalidChunkSize      = errors.New("invalid chunk size")
	ErrUploadIncomplete      = errors.New("upload incomplete")
	ErrInvalidFileType       = errors.New("only mp4 files are supported")
	ErrFileTooLarge          = errors.New("file exceeds maximum upload size")
)

type ChunkUploadService struct {
	cfg    *config.Config
	videos *VideoService
}

func NewChunkUploadService(cfg *config.Config, videos *VideoService) *ChunkUploadService {
	return &ChunkUploadService{cfg: cfg, videos: videos}
}

type InitChunkUploadRequest struct {
	FileName    string  `json:"fileName" binding:"required"`
	FileSize    int64   `json:"fileSize" binding:"required,gt=0"`
	ChunkSize   int64   `json:"chunkSize"`
	Title       string  `json:"title"`
	Description *string `json:"description"`
}

type InitChunkUploadResult struct {
	UploadID    uuid.UUID `json:"uploadId"`
	ChunkSize   int64     `json:"chunkSize"`
	TotalChunks int       `json:"totalChunks"`
}

type ChunkUploadStatus struct {
	UploadID       uuid.UUID `json:"uploadId"`
	FileName       string    `json:"fileName"`
	FileSize       int64     `json:"fileSize"`
	ChunkSize      int64     `json:"chunkSize"`
	TotalChunks    int       `json:"totalChunks"`
	UploadedChunks []int     `json:"uploadedChunks"`
	Completed      bool      `json:"completed"`
}

type chunkUploadMeta struct {
	UploadID    uuid.UUID `json:"uploadId"`
	FileName    string    `json:"fileName"`
	FileSize    int64     `json:"fileSize"`
	ChunkSize   int64     `json:"chunkSize"`
	TotalChunks int       `json:"totalChunks"`
	Title       string    `json:"title"`
	Description *string   `json:"description,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

func (s *ChunkUploadService) Init(ctx context.Context, req InitChunkUploadRequest) (*InitChunkUploadResult, error) {
	fileName := filepath.Base(strings.TrimSpace(req.FileName))
	if fileName == "" || !strings.EqualFold(filepath.Ext(fileName), ".mp4") {
		return nil, ErrInvalidFileType
	}
	if req.FileSize > s.cfg.MaxUploadFileSize {
		return nil, ErrFileTooLarge
	}

	chunkSize := req.ChunkSize
	if chunkSize <= 0 {
		chunkSize = s.cfg.ChunkSize
	}
	if chunkSize < 1024*1024 || chunkSize > 50*1024*1024 {
		return nil, ErrInvalidChunkSize
	}

	totalChunks := int((req.FileSize + chunkSize - 1) / chunkSize)
	uploadID := uuid.New()

	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = strings.TrimSuffix(fileName, filepath.Ext(fileName))
	}

	meta := chunkUploadMeta{
		UploadID:    uploadID,
		FileName:    fileName,
		FileSize:    req.FileSize,
		ChunkSize:   chunkSize,
		TotalChunks: totalChunks,
		Title:       title,
		Description: req.Description,
		CreatedAt:   time.Now(),
	}

	dir := s.sessionDir(uploadID)
	if err := os.MkdirAll(filepath.Join(dir, "chunks"), 0o755); err != nil {
		return nil, fmt.Errorf("create upload dir: %w", err)
	}
	if err := s.saveMeta(dir, meta); err != nil {
		return nil, err
	}

	return &InitChunkUploadResult{
		UploadID:    uploadID,
		ChunkSize:   chunkSize,
		TotalChunks: totalChunks,
	}, nil
}

func (s *ChunkUploadService) UploadChunk(ctx context.Context, uploadID uuid.UUID, index int, r io.Reader) error {
	meta, dir, err := s.loadSession(uploadID)
	if err != nil {
		return err
	}
	if index < 0 || index >= meta.TotalChunks {
		return ErrInvalidChunkIndex
	}

	chunkPath := s.chunkPath(dir, index)
	tmpPath := chunkPath + ".tmp"

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open chunk file: %w", err)
	}

	written, err := io.Copy(f, r)
	closeErr := f.Close()
	if err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write chunk: %w", err)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close chunk file: %w", closeErr)
	}

	expected := meta.ChunkSize
	if index == meta.TotalChunks-1 {
		remainder := meta.FileSize % meta.ChunkSize
		if remainder > 0 {
			expected = remainder
		}
	}
	if written != expected {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("%w: chunk %d expected %d bytes, got %d", ErrInvalidChunkSize, index, expected, written)
	}

	if err := os.Rename(tmpPath, chunkPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("finalize chunk: %w", err)
	}
	return nil
}

func (s *ChunkUploadService) Status(ctx context.Context, uploadID uuid.UUID) (*ChunkUploadStatus, error) {
	meta, dir, err := s.loadSession(uploadID)
	if err != nil {
		return nil, err
	}

	uploaded, err := s.listUploadedChunks(dir, meta.TotalChunks)
	if err != nil {
		return nil, err
	}

	return &ChunkUploadStatus{
		UploadID:       uploadID,
		FileName:       meta.FileName,
		FileSize:       meta.FileSize,
		ChunkSize:      meta.ChunkSize,
		TotalChunks:    meta.TotalChunks,
		UploadedChunks: uploaded,
		Completed:      len(uploaded) == meta.TotalChunks,
	}, nil
}

func (s *ChunkUploadService) Complete(ctx context.Context, uploadID uuid.UUID) (uuid.UUID, error) {
	meta, dir, err := s.loadSession(uploadID)
	if err != nil {
		return uuid.Nil, err
	}

	uploaded, err := s.listUploadedChunks(dir, meta.TotalChunks)
	if err != nil {
		return uuid.Nil, err
	}
	if len(uploaded) != meta.TotalChunks {
		return uuid.Nil, ErrUploadIncomplete
	}

	filename := fmt.Sprintf("%d_%s", time.Now().UnixNano(), meta.FileName)
	dest := filepath.Join(s.cfg.UploadDir, filename)

	if err := s.mergeChunks(dir, meta, dest); err != nil {
		return uuid.Nil, err
	}

	videoID, err := s.videos.Upload(ctx, meta.Title, meta.Description, dest)
	if err != nil {
		_ = os.Remove(dest)
		return uuid.Nil, err
	}

	_ = os.RemoveAll(dir)
	return videoID, nil
}

func (s *ChunkUploadService) mergeChunks(dir string, meta chunkUploadMeta, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create upload dir: %w", err)
	}

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create merged file: %w", err)
	}
	defer out.Close()

	var total int64
	for i := 0; i < meta.TotalChunks; i++ {
		chunkPath := s.chunkPath(dir, i)
		in, err := os.Open(chunkPath)
		if err != nil {
			return fmt.Errorf("open chunk %d: %w", i, err)
		}
		n, err := io.Copy(out, in)
		_ = in.Close()
		if err != nil {
			return fmt.Errorf("merge chunk %d: %w", i, err)
		}
		total += n
	}

	if total != meta.FileSize {
		_ = os.Remove(dest)
		return fmt.Errorf("merged size mismatch: expected %d, got %d", meta.FileSize, total)
	}
	return nil
}

func (s *ChunkUploadService) loadSession(uploadID uuid.UUID) (chunkUploadMeta, string, error) {
	dir := s.sessionDir(uploadID)
	meta, err := s.readMeta(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return chunkUploadMeta{}, "", ErrUploadSessionNotFound
		}
		return chunkUploadMeta{}, "", err
	}
	if time.Since(meta.CreatedAt) > s.cfg.ChunkUploadTTL {
		_ = os.RemoveAll(dir)
		return chunkUploadMeta{}, "", ErrUploadSessionExpired
	}
	return meta, dir, nil
}

func (s *ChunkUploadService) sessionDir(uploadID uuid.UUID) string {
	return filepath.Join(s.cfg.TempDir, "chunk-uploads", uploadID.String())
}

func (s *ChunkUploadService) chunkPath(dir string, index int) string {
	return filepath.Join(dir, "chunks", fmt.Sprintf("%d.part", index))
}

func (s *ChunkUploadService) metaPath(dir string) string {
	return filepath.Join(dir, "meta.json")
}

func (s *ChunkUploadService) saveMeta(dir string, meta chunkUploadMeta) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal meta: %w", err)
	}
	return os.WriteFile(s.metaPath(dir), data, 0o644)
}

func (s *ChunkUploadService) readMeta(dir string) (chunkUploadMeta, error) {
	data, err := os.ReadFile(s.metaPath(dir))
	if err != nil {
		return chunkUploadMeta{}, err
	}
	var meta chunkUploadMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return chunkUploadMeta{}, fmt.Errorf("parse meta: %w", err)
	}
	return meta, nil
}

func (s *ChunkUploadService) listUploadedChunks(dir string, total int) ([]int, error) {
	var uploaded []int
	for i := 0; i < total; i++ {
		if _, err := os.Stat(s.chunkPath(dir, i)); err == nil {
			uploaded = append(uploaded, i)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("stat chunk %d: %w", i, err)
		}
	}
	return uploaded, nil
}
