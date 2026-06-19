package repository

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zood/video-manager/internal/model"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type DB struct {
	*gorm.DB
}

func NewDB(databaseURL string) (*DB, error) {
	dsn := ensurePgBouncerCompat(databaseURL)

	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN:                  dsn,
		PreferSimpleProtocol: true,
	}), &gorm.Config{
		PrepareStmt: false,
	})
	if err != nil {
		return nil, fmt.Errorf("connect database: %w", err)
	}
	return &DB{DB: db}, nil
}

// Supabase pooler (6543) 不支持 GORM 预编译语句，需禁用。
func ensurePgBouncerCompat(dsn string) string {
	if strings.Contains(dsn, "prefer_simple_protocol=") {
		return dsn
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "prefer_simple_protocol=true"
}

type VideoRepository struct {
	db *gorm.DB
}

func NewVideoRepository(db *DB) *VideoRepository {
	return &VideoRepository{db: db.DB}
}

func (r *VideoRepository) Create(video *model.Video) error {
	if video.ID == uuid.Nil {
		video.ID = uuid.New()
	}
	return r.db.Create(video).Error
}

func (r *VideoRepository) FindByID(id uuid.UUID) (*model.Video, error) {
	var video model.Video
	if err := r.db.First(&video, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &video, nil
}

func (r *VideoRepository) List(offset, limit int) ([]model.Video, int64, error) {
	var videos []model.Video
	var total int64

	q := r.db.Model(&model.Video{})
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&videos).Error; err != nil {
		return nil, 0, err
	}
	return videos, total, nil
}

func (r *VideoRepository) UpdateStatus(id uuid.UUID, status string, errMsg *string) error {
	updates := map[string]interface{}{
		"status":     status,
		"updated_at": time.Now(),
	}
	if errMsg != nil {
		updates["error_message"] = *errMsg
	}
	return r.db.Model(&model.Video{}).Where("id = ?", id).Updates(updates).Error
}

func (r *VideoRepository) UpdateAfterTranscode(id uuid.UUID, updates map[string]interface{}) error {
	updates["updated_at"] = time.Now()
	return r.db.Model(&model.Video{}).Where("id = ?", id).Updates(updates).Error
}

type UsageRepository struct {
	db *gorm.DB
}

func NewUsageRepository(db *DB) *UsageRepository {
	return &UsageRepository{db: db.DB}
}

func (r *UsageRepository) GetUsage(userID string, date time.Time) (*model.UserVideoUsage, error) {
	var usage model.UserVideoUsage
	err := r.db.Where("user_id = ? AND usage_date = ?", userID, date.Format("2006-01-02")).First(&usage).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &usage, nil
}

func (r *UsageRepository) UpsertUsage(userID string, date time.Time, watchSeconds int) error {
	d := date.Format("2006-01-02")
	return r.db.Exec(`
		INSERT INTO user_video_usage (user_id, usage_date, watch_seconds, updated_at)
		VALUES (?, ?::date, ?, NOW())
		ON CONFLICT (user_id, usage_date)
		DO UPDATE SET watch_seconds = EXCLUDED.watch_seconds, updated_at = NOW()
	`, userID, d, watchSeconds).Error
}

func (r *UsageRepository) CreateAccessLog(log *model.VideoAccessLog) error {
	return r.db.Create(log).Error
}

type UserWatchSummary struct {
	UserID            string    `gorm:"column:user_id" json:"userId"`
	TotalWatchSeconds int       `gorm:"column:total_watch_seconds" json:"totalWatchSeconds"`
	LastWatchedAt     time.Time `gorm:"column:last_watched_at" json:"lastWatchedAt"`
}

func (r *UsageRepository) SummarizeByVideo(videoID uuid.UUID) ([]UserWatchSummary, error) {
	var rows []UserWatchSummary
	err := r.db.Raw(`
		SELECT user_id,
		       COALESCE(SUM(watch_seconds), 0) AS total_watch_seconds,
		       MAX(created_at) AS last_watched_at
		FROM video_access_logs
		WHERE video_id = ? AND watch_seconds IS NOT NULL
		GROUP BY user_id
		ORDER BY last_watched_at DESC
	`, videoID).Scan(&rows).Error
	return rows, err
}

func (r *UsageRepository) ListAccessLogsByVideo(videoID uuid.UUID, limit int) ([]model.VideoAccessLog, error) {
	var logs []model.VideoAccessLog
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	err := r.db.Where("video_id = ?", videoID).
		Order("created_at DESC").
		Limit(limit).
		Find(&logs).Error
	return logs, err
}
